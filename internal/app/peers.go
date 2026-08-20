package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/namechenxinyu/lan-share/internal/fileutil"
	"github.com/namechenxinyu/lan-share/internal/security"
)

func (a *App) peerRequest(r *http.Request, deviceID, method, path string, body io.Reader) (*http.Request, error) {
	dev, ok := a.discovery.Find(deviceID)
	if !ok {
		return nil, fmt.Errorf("device not found or offline")
	}
	u := fmt.Sprintf("http://%s:%d%s", dev.IP, dev.Port, path)
	req, err := http.NewRequestWithContext(r.Context(), method, u, body)
	if err != nil {
		return nil, err
	}
	if cred, ok := a.security.Credential(deviceID); ok {
		req.Header.Set("Authorization", "Bearer "+cred.Token)
		req.Header.Set("X-LAN-Device-ID", a.security.DeviceID())
	}
	return req, nil
}

func (a *App) handleRelayInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	body, err := ensureNoExtra(r.Body, 64<<10)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req, err := a.peerRequest(r, deviceID, http.MethodPost, "/api/uploads/init", bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	a.forwardPeerResponse(w, req)
}

func (a *App) handleRelayChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	offset := strings.TrimSpace(r.URL.Query().Get("offset"))
	plain, err := ensureNoExtra(r.Body, maxChunkSize)
	if err != nil {
		http.Error(w, err.Error(), 413)
		return
	}
	path := "/api/uploads/chunk?id=" + urlQuery(id) + "&offset=" + urlQuery(offset)
	var body io.Reader = bytes.NewReader(plain)
	encrypted := false
	req, err := a.peerRequest(r, deviceID, http.MethodPut, path, body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if cred, ok := a.security.Credential(deviceID); ok {
		ciphertext, nonce, encErr := encryptChunk(cred.Token, plain)
		if encErr != nil {
			http.Error(w, encErr.Error(), 500)
			return
		}
		encrypted = true
		req.Body = io.NopCloser(bytes.NewReader(ciphertext))
		req.ContentLength = int64(len(ciphertext))
		req.Header.Set("X-LAN-Encrypted", "aes-gcm-chunk-v1")
		req.Header.Set("X-LAN-Nonce", encodeNonce(nonce))
		req.Header.Set("X-LAN-Plain-Length", strconv.Itoa(len(plain)))
	} else {
		req.ContentLength = int64(len(plain))
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if encrypted {
		w.Header().Set("X-LAN-Encrypted", "aes-gcm-chunk-v1")
	}
	a.forwardPeerResponse(w, req)
}

func (a *App) handleRelayComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	dev, ok := a.discovery.Find(deviceID)
	if !ok {
		http.Error(w, "device not found", 404)
		return
	}
	req, err := a.peerRequest(r, deviceID, http.MethodPost, "/api/uploads/complete?id="+urlQuery(id), nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	resp, err := a.peerClient.Do(req)
	if err != nil {
		http.Error(w, "peer request failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var v struct {
			Name    string  `json:"name"`
			Size    int64   `json:"size"`
			Seconds float64 `json:"seconds"`
		}
		if json.Unmarshal(b, &v) == nil {
			speed := float64(0)
			if v.Seconds > 0 {
				speed = float64(v.Size) / v.Seconds
			}
			a.history.add(TransferRecord{Direction: "send", Peer: dev.Name, Name: v.Name, Size: v.Size, StartedAt: time.Now().Unix() - int64(v.Seconds), Status: "completed", BytesPerSecond: speed, Encrypted: a.security.IsPaired(deviceID)})
		}
	}
}

func (a *App) forwardPeerResponse(w http.ResponseWriter, req *http.Request) {
	resp, err := a.peerClient.Do(req)
	if err != nil {
		http.Error(w, "peer request failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 2<<20))
}

func (a *App) handlePeerFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	req, err := a.peerRequest(r, deviceID, http.MethodGet, "/api/files", nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a.forwardPeerResponse(w, req)
}

func (a *App) handleParallelPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Name     string `json:"name"`
		Parallel int    `json:"parallel"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	name, err := fileutil.CleanFileName(body.Name)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Parallel <= 0 {
		body.Parallel = 4
	}
	if body.Parallel > 8 {
		body.Parallel = 8
	}
	dev, ok := a.discovery.Find(body.DeviceID)
	if !ok {
		http.Error(w, "device not found", 404)
		return
	}
	size, err := a.remoteFileSize(r, body.DeviceID, name)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	dir := a.ShareDir()
	tmp, err := os.CreateTemp(dir, ".lanshare-pull-*.part")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }()
	if err = tmp.Truncate(size); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	start := time.Now()
	chunk := int64(8 << 20)
	jobs := make(chan [2]int64, body.Parallel*2)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	var total atomic.Int64
	cred, _ := a.security.Credential(body.DeviceID)
	encrypted := cred.Token != ""
	setErr := func(e error) {
		if e == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		errMu.Unlock()
	}
	hasErr := func() bool { errMu.Lock(); defer errMu.Unlock(); return firstErr != nil }
	worker := func() {
		defer wg.Done()
		buf := make([]byte, 1<<20)
		for rg := range jobs {
			if hasErr() {
				continue
			}
			var data []byte
			var e error
			if encrypted {
				data, e = a.fetchEncryptedRange(r, body.DeviceID, name, rg[0], rg[1], cred.Token)
			} else {
				e = a.fetchPlainRange(r, body.DeviceID, name, rg[0], rg[1], tmp, buf, &total)
			}
			if e != nil {
				setErr(e)
				continue
			}
			if encrypted {
				if _, e = tmp.WriteAt(data, rg[0]); e == nil {
					total.Add(int64(len(data)))
				} else {
					setErr(e)
				}
			}
		}
	}
	for i := 0; i < body.Parallel; i++ {
		wg.Add(1)
		go worker()
	}
	for startOff := int64(0); startOff < size; startOff += chunk {
		end := startOff + chunk - 1
		if end >= size {
			end = size - 1
		}
		jobs <- [2]int64{startOff, end}
	}
	close(jobs)
	wg.Wait()
	errMu.Lock()
	pullErr := firstErr
	errMu.Unlock()
	if pullErr != nil {
		http.Error(w, pullErr.Error(), 502)
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
	elapsed := time.Since(start).Seconds()
	speed := float64(size) / elapsed
	a.history.add(TransferRecord{Direction: "pull", Peer: dev.Name, Name: filepath.Base(final), Size: size, StartedAt: start.Unix(), Status: "completed", BytesPerSecond: speed, Encrypted: encrypted})
	writeJSON(w, 201, map[string]any{"name": filepath.Base(final), "size": size, "seconds": elapsed, "bytes_per_second": speed, "parallel": body.Parallel, "encrypted": encrypted})
}

func (a *App) remoteFileSize(r *http.Request, deviceID, name string) (int64, error) {
	req, err := a.peerRequest(r, deviceID, http.MethodHead, "/api/download?name="+urlQuery(name), nil)
	if err != nil {
		return 0, err
	}
	resp, err := a.peerClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("peer: %s", strings.TrimSpace(string(b)))
	}
	n := resp.ContentLength
	if n < 0 {
		n = parseContentLength(resp.Header.Get("Content-Length"))
	}
	if n < 0 {
		return 0, fmt.Errorf("peer did not provide file size")
	}
	return n, nil
}

func (a *App) fetchPlainRange(r *http.Request, deviceID, name string, start, end int64, f *os.File, buf []byte, total *atomic.Int64) error {
	req, err := a.peerRequest(r, deviceID, http.MethodGet, "/api/download?name="+urlQuery(name), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := a.peerClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && !(start == 0 && resp.StatusCode == http.StatusOK) {
		return fmt.Errorf("peer range HTTP %d", resp.StatusCode)
	}
	ow := &offsetWriter{f: f, off: start, total: total}
	_, err = io.CopyBuffer(ow, resp.Body, buf)
	return err
}

type offsetWriter struct {
	f     *os.File
	off   int64
	total *atomic.Int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	n, err := w.f.WriteAt(p, w.off)
	w.off += int64(n)
	w.total.Add(int64(n))
	return n, err
}

func (a *App) handleSecureRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	token := bearerToken(r)
	if _, ok := a.security.VerifyToken(token); !ok {
		http.Error(w, "paired token required", 401)
		return
	}
	name, err := fileutil.CleanFileName(r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	start, err1 := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	end, err2 := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
	if err1 != nil || err2 != nil || start < 0 || end < start || end-start+1 > 8<<20 {
		http.Error(w, "invalid range", 400)
		return
	}
	f, err := os.Open(filepath.Join(a.ShareDir(), name))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || end >= info.Size() {
		http.Error(w, "range outside file", 416)
		return
	}
	plain := make([]byte, end-start+1)
	if _, err = io.ReadFull(io.NewSectionReader(f, start, int64(len(plain))), plain); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ciphertext, nonce, err := encryptChunk(token, plain)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-LAN-Encrypted", "aes-gcm-chunk-v1")
	w.Header().Set("X-LAN-Nonce", encodeNonce(nonce))
	w.Header().Set("X-LAN-Plain-Length", strconv.Itoa(len(plain)))
	w.WriteHeader(200)
	_, _ = w.Write(ciphertext)
}

func (a *App) fetchEncryptedRange(r *http.Request, deviceID, name string, start, end int64, token string) ([]byte, error) {
	req, err := a.peerRequest(r, deviceID, http.MethodGet, fmt.Sprintf("/api/secure-range?name=%s&start=%d&end=%d", urlQuery(name), start, end), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.peerClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("peer: %s", strings.TrimSpace(string(b)))
	}
	nonce, err := base64.RawURLEncoding.DecodeString(resp.Header.Get("X-LAN-Nonce"))
	if err != nil {
		return nil, err
	}
	ciphertext, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+64))
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("invalid encrypted range nonce")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func urlQuery(s string) string { return url.QueryEscape(s) }
