package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/namechenxinyu/lan-share/internal/config"
	"github.com/namechenxinyu/lan-share/internal/security"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Runtime{
		ShareDir:      dir,
		Name:          "test",
		Port:          51888,
		DiscoveryPort: 51889,
		ConfigPath:    filepath.Join(t.TempDir(), "config.json"),
	}
	cfg.SecurityPath = filepath.Join(t.TempDir(), "security.json")
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func localReq(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = "127.0.0.1:12345"
	return r
}

func TestUploadAndRangeDownload(t *testing.T) {
	a := newTestApp(t)
	payload := bytes.Repeat([]byte("0123456789abcdef"), 256*1024) // 4 MiB

	req := localReq(http.MethodPut, "/api/upload?name=large.bin", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	a.handleUploadCompat(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}

	stored, err := os.ReadFile(filepath.Join(a.ShareDir(), "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatal("stored payload mismatch")
	}

	req = localReq(http.MethodGet, "/api/download?name=large.bin", nil)
	req.Header.Set("Range", "bytes=1048576-2097151")
	w = httptest.NewRecorder()
	a.handleDownload(w, req)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d body=%s", w.Code, w.Body.String())
	}
	want := payload[1048576:2097152]
	got, _ := io.ReadAll(w.Result().Body)
	if !bytes.Equal(got, want) {
		t.Fatal("range payload mismatch")
	}
	if !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes 1048576-2097151/") {
		t.Fatalf("bad Content-Range: %q", w.Header().Get("Content-Range"))
	}
}

func TestUploadDoesNotOverwrite(t *testing.T) {
	a := newTestApp(t)
	if err := os.WriteFile(filepath.Join(a.ShareDir(), "same.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	req := localReq(http.MethodPut, "/api/upload?name=same.txt", bytes.NewBufferString("new"))
	w := httptest.NewRecorder()
	a.handleUploadCompat(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(a.ShareDir(), "same (1).txt"))
	if err != nil || string(b) != "new" {
		t.Fatalf("renamed upload missing: err=%v body=%q", err, string(b))
	}
}

func TestSwitchShareDirShowsExistingFiles(t *testing.T) {
	a := newTestApp(t)
	newDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(newDir, "existing.iso"), []byte("already here"), 0644); err != nil {
		t.Fatal(err)
	}
	body := `{"path":` + strconv.Quote(newDir) + `}`
	req := localReq(http.MethodPut, "/api/share-dir", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleShareDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("switch status=%d body=%s", w.Code, w.Body.String())
	}
	if got := a.ShareDir(); got != newDir {
		t.Fatalf("share dir=%q want=%q", got, newDir)
	}

	req = localReq(http.MethodGet, "/api/files", nil)
	w = httptest.NewRecorder()
	a.handleFiles(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "existing.iso") {
		t.Fatalf("existing file not listed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadUsesSwitchedShareDir(t *testing.T) {
	a := newTestApp(t)
	newDir := t.TempDir()
	body := `{"path":` + strconv.Quote(newDir) + `}`
	req := localReq(http.MethodPut, "/api/share-dir", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleShareDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("switch status=%d body=%s", w.Code, w.Body.String())
	}

	req = localReq(http.MethodPut, "/api/upload?name=after-switch.bin", strings.NewReader("payload"))
	w = httptest.NewRecorder()
	a.handleUploadCompat(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(newDir, "after-switch.bin"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("switched upload missing: err=%v body=%q", err, string(b))
	}
}

func TestSwitchShareDirPersists(t *testing.T) {
	a := newTestApp(t)
	newDir := t.TempDir()
	body := `{"path":` + strconv.Quote(newDir) + `}`
	req := localReq(http.MethodPut, "/api/share-dir", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleShareDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("switch status=%d body=%s", w.Code, w.Body.String())
	}
	got, err := config.LoadShareDir(a.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != newDir {
		t.Fatalf("persisted dir=%q want=%q", got, newDir)
	}
}

func TestRemoteCannotManageOrProxy(t *testing.T) {
	a := newTestApp(t)
	newDir := t.TempDir()
	body := `{"path":` + strconv.Quote(newDir) + `}`
	req := httptest.NewRequest(http.MethodPut, "/api/share-dir", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:12345"
	w := httptest.NewRecorder()
	a.handleShareDir(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote switch status=%d", w.Code)
	}

	if err := os.WriteFile(filepath.Join(a.ShareDir(), "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/files?name=keep.txt", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	w = httptest.NewRecorder()
	a.handleFiles(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote delete status=%d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(a.ShareDir(), "keep.txt")); err != nil {
		t.Fatalf("file was removed: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/relay/init?device_id=x", strings.NewReader(`{"id":"abcdefgh","name":"x.bin","size":1}`))
	req.RemoteAddr = "192.0.2.10:12345"
	w = httptest.NewRecorder()
	a.handleRelayInit(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote proxy status=%d", w.Code)
	}
}

func TestRemoteInfoDoesNotLeakFilesystemPath(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	w := httptest.NewRecorder()
	a.handleInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if strings.Contains(w.Body.String(), a.ShareDir()) {
		t.Fatalf("remote info leaked share path: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"can_manage":false`) {
		t.Fatalf("expected remote management=false: %s", w.Body.String())
	}
}

func TestResumableUpload(t *testing.T) {
	a := newTestApp(t)
	payload := bytes.Repeat([]byte("resume-data-"), 1024*128)
	id := "resume12345678"

	body := fmt.Sprintf(`{"id":%q,"name":"resume.bin","size":%d}`, id, len(payload))
	req := localReq(http.MethodPost, "/api/uploads/init", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleUploadInit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("init: %d %s", w.Code, w.Body.String())
	}

	cut := len(payload) / 2
	req = localReq(http.MethodPut, "/api/uploads/chunk?id="+id+"&offset=0", bytes.NewReader(payload[:cut]))
	req.ContentLength = int64(cut)
	w = httptest.NewRecorder()
	a.handleUploadChunk(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chunk1: %d %s", w.Code, w.Body.String())
	}

	// Re-init the same stable upload id: server must report the saved offset.
	req = localReq(http.MethodPost, "/api/uploads/init", strings.NewReader(body))
	w = httptest.NewRecorder()
	a.handleUploadInit(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`"offset":%d`, cut)) {
		t.Fatalf("resume init: %d %s", w.Code, w.Body.String())
	}

	req = localReq(http.MethodPut, fmt.Sprintf("/api/uploads/chunk?id=%s&offset=%d", id, cut), bytes.NewReader(payload[cut:]))
	req.ContentLength = int64(len(payload) - cut)
	w = httptest.NewRecorder()
	a.handleUploadChunk(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chunk2: %d %s", w.Code, w.Body.String())
	}

	req = localReq(http.MethodPost, "/api/uploads/complete?id="+id, nil)
	w = httptest.NewRecorder()
	a.handleUploadComplete(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(a.ShareDir(), "resume.bin"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed file mismatch: %v", err)
	}
}

func TestSecureModeRequiresPairingForRemoteFiles(t *testing.T) {
	a := newTestApp(t)
	if err := a.setSecureMode(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.ShareDir(), "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	req.RemoteAddr = "192.0.2.20:5555"
	w := httptest.NewRecorder()
	a.handleFiles(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEncryptedChunkRoundTrip(t *testing.T) {
	a := newTestApp(t)
	if err := a.setSecureMode(true); err != nil {
		t.Fatal(err)
	}
	peer, err := a.security.Trust("peer-1", "peer")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 250, 251}, 1024)
	ciphertext, nonce, err := encryptChunk(peer.Token, payload)
	if err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"id":"encrypt123456","name":"encrypted.bin","size":%d}`, len(payload))
	req := httptest.NewRequest(http.MethodPost, "/api/uploads/init", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.20:5555"
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	w := httptest.NewRecorder()
	a.handleUploadInit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("init=%d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/uploads/chunk?id=encrypt123456&offset=0", bytes.NewReader(ciphertext))
	req.RemoteAddr = "192.0.2.20:5555"
	req.ContentLength = int64(len(ciphertext))
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	req.Header.Set("X-LAN-Encrypted", "aes-gcm-chunk-v1")
	req.Header.Set("X-LAN-Nonce", encodeNonce(nonce))
	w = httptest.NewRecorder()
	a.handleUploadChunk(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chunk=%d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/uploads/complete?id=encrypt123456", nil)
	req.RemoteAddr = "192.0.2.20:5555"
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	w = httptest.NewRecorder()
	a.handleUploadComplete(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("complete=%d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(a.ShareDir(), "encrypted.bin"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("decrypted file mismatch: %v", err)
	}
}

func TestSecureRangeEncrypted(t *testing.T) {
	a := newTestApp(t)
	peer, err := a.security.Trust("peer-2", "peer2")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("abcdefgh"), 1024)
	if err := os.WriteFile(filepath.Join(a.ShareDir(), "range.bin"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/secure-range?name=range.bin&start=100&end=999", nil)
	req.RemoteAddr = "192.0.2.21:5555"
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	w := httptest.NewRecorder()
	a.handleSecureRange(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("secure range=%d %s", w.Code, w.Body.String())
	}
	key := security.KeyFromToken(peer.Token)
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	nonce, err := base64.RawURLEncoding.DecodeString(w.Header().Get("X-LAN-Nonce"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, nonce, w.Body.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, payload[100:1000]) {
		t.Fatal("secure range plaintext mismatch")
	}
}
