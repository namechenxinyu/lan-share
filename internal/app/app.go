package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/namechenxinyu/lan-share/internal/config"
	"github.com/namechenxinyu/lan-share/internal/discovery"
	"github.com/namechenxinyu/lan-share/internal/fileutil"
	"github.com/namechenxinyu/lan-share/internal/platform"
	"github.com/namechenxinyu/lan-share/internal/security"
	"github.com/namechenxinyu/lan-share/internal/webui"
)

const Version = "0.6.0"

const maxChunkSize = 64 << 20

type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
}

type App struct {
	cfg config.Runtime

	stateMu    sync.RWMutex
	shareDir   string
	name       string
	secureMode bool

	finalizeMu sync.Mutex
	bufPool    sync.Pool
	discovery  *discovery.Service
	peerClient *http.Client
	security   *security.Manager
	history    *historyStore

	sessionsMu sync.Mutex
	sessions   map[string]*uploadSession
	attemptsMu sync.Mutex
	attempts   map[string]*pairAttempt
}

func New(cfg config.Runtime) (*App, error) {
	sec, err := security.New(cfg.SecurityPath)
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg: cfg, shareDir: cfg.ShareDir, name: cfg.Name, secureMode: cfg.SecureMode,
		security: sec, history: newHistory(100), sessions: make(map[string]*uploadSession), attempts: make(map[string]*pairAttempt),
	}
	a.discovery = discovery.New(sec.DeviceID(), cfg.Name, cfg.Port, cfg.DiscoveryPort)
	a.discovery.SetSecure(cfg.SecureMode)
	a.bufPool.New = func() any { b := make([]byte, 1024*1024); return &b }
	a.peerClient = &http.Client{Transport: &http.Transport{
		Proxy:              nil,
		DialContext:        (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		DisableCompression: true, MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
		IdleConnTimeout: 90 * time.Second, ExpectContinueTimeout: time.Second,
	}}
	go a.cleanupPartialUploads()
	return a, nil
}

func (a *App) StartDiscovery() error { return a.discovery.Start() }
func (a *App) Close() error {
	if tr, ok := a.peerClient.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return a.discovery.Close()
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, err := fs.Sub(webui.Assets, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/api/info", a.handleInfo)
	mux.HandleFunc("/api/devices", a.handleDevices)
	mux.HandleFunc("/api/files", a.handleFiles)
	mux.HandleFunc("/api/download", a.handleDownload)
	mux.HandleFunc("/api/secure-range", a.handleSecureRange)
	mux.HandleFunc("/api/upload", a.handleUploadCompat)
	mux.HandleFunc("/api/uploads/init", a.handleUploadInit)
	mux.HandleFunc("/api/uploads/chunk", a.handleUploadChunk)
	mux.HandleFunc("/api/uploads/complete", a.handleUploadComplete)
	mux.HandleFunc("/api/uploads/abort", a.handleUploadAbort)
	mux.HandleFunc("/api/relay/init", a.handleRelayInit)
	mux.HandleFunc("/api/relay/chunk", a.handleRelayChunk)
	mux.HandleFunc("/api/relay/complete", a.handleRelayComplete)
	mux.HandleFunc("/api/peer-files", a.handlePeerFiles)
	mux.HandleFunc("/api/pull", a.handleParallelPull)
	mux.HandleFunc("/api/history", a.handleHistory)
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/share-dir", a.handleShareDir)
	mux.HandleFunc("/api/open-dir", a.handleOpenDir)
	mux.HandleFunc("/api/pair", a.handlePair)
	mux.HandleFunc("/api/pair-device", a.handlePairDevice)
	mux.HandleFunc("/api/browser-pair", a.handleBrowserPair)
	mux.HandleFunc("/api/security", a.handleSecurity)
	mux.HandleFunc("/api/trust/revoke", a.handleRevokeTrust)
	mux.HandleFunc("/api/update-check", a.handleUpdateCheck)
	return a.withCommonHeaders(mux)
}

func (a *App) LocalURL() string { return fmt.Sprintf("http://127.0.0.1:%d", a.cfg.Port) }
func (a *App) LANURLs() []string {
	ips := discovery.LocalIPv4s()
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, fmt.Sprintf("http://%s:%d", ip, a.cfg.Port))
	}
	return out
}
func (a *App) ShareDir() string { a.stateMu.RLock(); defer a.stateMu.RUnlock(); return a.shareDir }
func (a *App) Name() string     { a.stateMu.RLock(); defer a.stateMu.RUnlock(); return a.name }
func (a *App) SecureMode() bool { a.stateMu.RLock(); defer a.stateMu.RUnlock(); return a.secureMode }

func (a *App) persistSettings() error {
	a.stateMu.RLock()
	s := config.Settings{ShareDir: a.shareDir, Name: a.name, SecureMode: a.secureMode}
	a.stateMu.RUnlock()
	return config.SaveSettings(a.cfg.ConfigPath, s)
}

func (a *App) setShareDir(dir string) error {
	a.stateMu.Lock()
	old := a.shareDir
	a.shareDir = dir
	a.stateMu.Unlock()
	if err := a.persistSettings(); err != nil {
		a.stateMu.Lock()
		a.shareDir = old
		a.stateMu.Unlock()
		return err
	}
	return nil
}
func (a *App) setName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("device name is empty")
	}
	if len([]rune(name)) > 64 {
		return fmt.Errorf("device name is too long")
	}
	a.stateMu.Lock()
	old := a.name
	a.name = name
	a.stateMu.Unlock()
	if err := a.persistSettings(); err != nil {
		a.stateMu.Lock()
		a.name = old
		a.stateMu.Unlock()
		return err
	}
	a.discovery.SetName(name)
	return nil
}
func (a *App) setSecureMode(v bool) error {
	a.stateMu.Lock()
	old := a.secureMode
	a.secureMode = v
	a.stateMu.Unlock()
	if err := a.persistSettings(); err != nil {
		a.stateMu.Lock()
		a.secureMode = old
		a.stateMu.Unlock()
		return err
	}
	a.discovery.SetSecure(v)
	return nil
}

func (a *App) withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' http:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webui.Assets.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": Version})
}
func (a *App) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	local := isLocalRequest(r)
	share := ""
	pair := ""
	if local {
		share = a.ShareDir()
		pair = a.security.PairCode()
	}
	writeJSON(w, 200, map[string]any{"id": a.security.DeviceID(), "name": a.Name(), "port": a.cfg.Port, "os": runtime.GOOS, "arch": runtime.GOARCH, "share_dir": share, "ips": discovery.LocalIPv4s(), "version": Version, "can_manage": local, "secure_mode": a.SecureMode(), "pair_code": pair, "autostart": platform.AutoStartEnabled()})
}
func (a *App) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	type view struct {
		discovery.Device
		Trusted bool `json:"trusted"`
	}
	devices := a.discovery.Devices()
	out := make([]view, 0, len(devices))
	for _, d := range devices {
		out = append(out, view{Device: d, Trusted: a.security.IsPaired(d.ID)})
	}
	writeJSON(w, 200, out)
}
func (a *App) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok := a.authorizeFileAccess(w, r); !ok {
			return
		}
		files, err := listFiles(a.ShareDir())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, files)
		return
	}
	if r.Method == http.MethodDelete {
		if !isLocalRequest(r) {
			http.Error(w, "local management only", 403)
			return
		}
		name, err := fileutil.CleanFileName(r.URL.Query().Get("name"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err = os.Remove(filepath.Join(a.ShareDir(), name)); err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
			} else {
				http.Error(w, err.Error(), 500)
			}
			return
		}
		w.WriteHeader(204)
		return
	}
	methodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
}
func listFiles(dir string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".lanshare-") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, FileInfo{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().Unix()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime == files[j].ModTime {
			return files[i].Name < files[j].Name
		}
		return files[i].ModTime > files[j].ModTime
	})
	return files, nil
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodHead)
		return
	}
	if _, ok := a.authorizeFileAccess(w, r); !ok {
		return
	}
	name, err := fileutil.CleanFileName(r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	path := filepath.Join(a.ShareDir(), name)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), 500)
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", 400)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", fileutil.ContentDisposition(name))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)
}
func (a *App) handleShareDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), 400)
		return
	}
	dir, err := config.PrepareShareDir(body.Path)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = a.setShareDir(dir); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"path": dir})
}
func (a *App) handleOpenDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	if err := platform.OpenPath(a.ShareDir()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	writeJSON(w, 200, a.history.list())
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	return discovery.IsLocalIP(net.ParseIP(host))
}
func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", 405)
}
