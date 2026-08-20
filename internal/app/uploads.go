package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/namechenxinyu/lan-share/internal/fileutil"
	"github.com/namechenxinyu/lan-share/internal/security"
)

var uploadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,96}$`)

type uploadSession struct {
	mu      sync.Mutex
	ID      string
	Name    string
	Size    int64
	Path    string
	Dir     string
	Offset  int64
	Started time.Time
	Peer    string
	Resumed bool
}

type uploadInitRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (a *App) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	token, ok := a.authorizeFileAccess(w, r)
	if !ok {
		return
	}
	var body uploadInitRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	peer := ""
	if p, valid := a.security.VerifyToken(token); valid {
		peer = p.Name
	}
	s, err := a.initUpload(body, peer)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, map[string]any{"id": s.ID, "offset": s.Offset, "size": s.Size, "chunk_size": 16 << 20, "resumed": s.Resumed})
}

func (a *App) initUpload(body uploadInitRequest, peer string) (*uploadSession, error) {
	body.ID = strings.TrimSpace(body.ID)
	if !uploadIDPattern.MatchString(body.ID) {
		return nil, errors.New("invalid upload id")
	}
	name, err := fileutil.CleanFileName(body.Name)
	if err != nil {
		return nil, err
	}
	if body.Size < 0 {
		return nil, errors.New("invalid file size")
	}
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if existing := a.sessions[body.ID]; existing != nil {
		if existing.Name != name || existing.Size != body.Size {
			return nil, errors.New("upload id already belongs to another file")
		}
		existing.Resumed = existing.Offset > 0
		return existing, nil
	}
	dir := a.ShareDir()
	path := filepath.Join(dir, ".lanshare-upload-"+body.ID+".part")
	offset := int64(0)
	resumed := false
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > body.Size {
			return nil, errors.New("invalid partial upload")
		}
		offset = info.Size()
		resumed = offset > 0
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600); err != nil {
		return nil, err
	}
	s := &uploadSession{ID: body.ID, Name: name, Size: body.Size, Path: path, Dir: dir, Offset: offset, Started: time.Now(), Peer: peer, Resumed: resumed}
	a.sessions[body.ID] = s
	return s, nil
}

func (a *App) getSession(id string) (*uploadSession, bool) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	s, ok := a.sessions[id]
	return s, ok
}
func (a *App) deleteSession(id string) {
	a.sessionsMu.Lock()
	delete(a.sessions, id)
	a.sessionsMu.Unlock()
}

func (a *App) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	token, ok := a.authorizeFileAccess(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s, ok := a.getSession(id)
	if !ok {
		http.Error(w, "upload session not found", 404)
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		http.Error(w, "invalid offset", 400)
		return
	}
	var reader io.Reader = r.Body
	encrypted := r.Header.Get("X-LAN-Encrypted") == "aes-gcm-chunk-v1"
	if encrypted {
		if _, valid := a.security.VerifyToken(token); !valid {
			http.Error(w, "paired token required for encrypted chunk", 401)
			return
		}
		plain, err := decryptChunk(token, r)
		if err != nil {
			http.Error(w, "decrypt chunk: "+err.Error(), 400)
			return
		}
		reader = bytes.NewReader(plain)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset != s.Offset {
		writeJSON(w, 409, map[string]any{"offset": s.Offset})
		return
	}
	if s.Offset >= s.Size {
		http.Error(w, "upload already complete", 409)
		return
	}
	f, err := os.OpenFile(s.Path, os.O_WRONLY, 0600)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	if _, err = f.Seek(s.Offset, io.SeekStart); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	limit := s.Size - s.Offset
	if limit > maxChunkSize {
		limit = maxChunkSize
	}
	if r.ContentLength > limit && !encrypted {
		http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
		return
	}
	originalOffset := s.Offset
	lr := &io.LimitedReader{R: reader, N: limit}
	bufp := a.bufPool.Get().(*[]byte)
	written, copyErr := io.CopyBuffer(f, lr, *bufp)
	a.bufPool.Put(bufp)
	if copyErr != nil {
		s.Offset += written
		w.Header().Set("X-LAN-Offset", strconv.FormatInt(s.Offset, 10))
		http.Error(w, "write chunk: "+copyErr.Error(), 400)
		return
	}
	if !encrypted && r.ContentLength < 0 {
		var extra [1]byte
		if n, _ := reader.Read(extra[:]); n > 0 {
			_ = f.Truncate(originalOffset)
			_, _ = f.Seek(originalOffset, io.SeekStart)
			http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	if written == 0 && s.Size != 0 {
		http.Error(w, "empty chunk", 400)
		return
	}
	s.Offset += written
	writeJSON(w, 200, map[string]any{"offset": s.Offset, "written": written, "encrypted": encrypted})
}

func decryptChunk(token string, r *http.Request) ([]byte, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(r.Header.Get("X-LAN-Nonce")))
	if err != nil {
		return nil, errors.New("invalid nonce")
	}
	key := security.KeyFromToken(token)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid nonce size")
	}
	ciphertext, err := io.ReadAll(io.LimitReader(r.Body, maxChunkSize+int64(gcm.Overhead())+1))
	if err != nil {
		return nil, err
	}
	if int64(len(ciphertext)) > maxChunkSize+int64(gcm.Overhead()) {
		return nil, errors.New("encrypted chunk too large")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func encryptChunk(token string, plain []byte) (ciphertext, nonce []byte, err error) {
	key := security.KeyFromToken(token)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plain, nil)
	return ciphertext, nonce, nil
}

func (a *App) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if _, ok := a.authorizeFileAccess(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s, ok := a.getSession(id)
	if !ok {
		http.Error(w, "upload session not found", 404)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Offset != s.Size {
		writeJSON(w, 409, map[string]any{"offset": s.Offset, "size": s.Size})
		return
	}
	f, err := os.OpenFile(s.Path, os.O_WRONLY, 0600)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if a.cfg.SyncWrites {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.finalizeMu.Lock()
	final := fileutil.UniqueDestination(s.Dir, s.Name)
	err = os.Rename(s.Path, final)
	a.finalizeMu.Unlock()
	if err != nil {
		http.Error(w, "finalize upload: "+err.Error(), 500)
		return
	}
	_ = os.Chmod(final, 0644)
	a.deleteSession(id)
	elapsed := time.Since(s.Started).Seconds()
	speed := float64(0)
	if elapsed > 0 {
		speed = float64(s.Size) / elapsed
	}
	peer := s.Peer
	if peer == "" {
		peer = "local/browser"
	}
	a.history.add(TransferRecord{Direction: "receive", Peer: peer, Name: filepath.Base(final), Size: s.Size, StartedAt: s.Started.Unix(), Status: "completed", BytesPerSecond: speed, Resumed: s.Resumed})
	writeJSON(w, 201, map[string]any{"name": filepath.Base(final), "size": s.Size, "seconds": elapsed, "bytes_per_second": speed, "resumed": s.Resumed})
}

func (a *App) handleUploadAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	if _, ok := a.authorizeFileAccess(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s, ok := a.getSession(id)
	if !ok {
		w.WriteHeader(204)
		return
	}
	s.mu.Lock()
	_ = os.Remove(s.Path)
	s.mu.Unlock()
	a.deleteSession(id)
	w.WriteHeader(204)
}

func (a *App) handleUploadCompat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	token, ok := a.authorizeFileAccess(w, r)
	if !ok {
		return
	}
	name, err := fileutil.CleanFileName(r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	peer := ""
	if p, valid := a.security.VerifyToken(token); valid {
		peer = p.Name
	}
	dir := a.ShareDir()
	tmp, err := os.CreateTemp(dir, ".lanshare-*.part")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	bufp := a.bufPool.Get().(*[]byte)
	start := time.Now()
	written, err := io.CopyBuffer(tmp, r.Body, *bufp)
	a.bufPool.Put(bufp)
	if err != nil {
		http.Error(w, "upload interrupted: "+err.Error(), 400)
		return
	}
	if a.cfg.SyncWrites {
		if err = tmp.Sync(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err = tmp.Close(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.finalizeMu.Lock()
	final := fileutil.UniqueDestination(dir, name)
	err = os.Rename(tmpPath, final)
	a.finalizeMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	keep = true
	elapsed := time.Since(start).Seconds()
	speed := float64(written) / elapsed
	a.history.add(TransferRecord{Direction: "receive", Peer: peer, Name: filepath.Base(final), Size: written, StartedAt: start.Unix(), Status: "completed", BytesPerSecond: speed})
	writeJSON(w, 201, map[string]any{"name": filepath.Base(final), "size": written, "bytes_per_second": speed})
}

func (a *App) cleanupPartialUploads() {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	cleanup := func() {
		dir := a.ShareDir()
		entries, _ := os.ReadDir(dir)
		cut := time.Now().Add(-7 * 24 * time.Hour)
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), ".lanshare-upload-") {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(cut) {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	cleanup()
	for range t.C {
		cleanup()
	}
}

func parseNonceHeader(resp *http.Response) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(resp.Header.Get("X-LAN-Nonce"))
}
func encodeNonce(n []byte) string       { return base64.RawURLEncoding.EncodeToString(n) }
func parseContentLength(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
func ensureNoExtra(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("payload too large")
	}
	return b, nil
}
