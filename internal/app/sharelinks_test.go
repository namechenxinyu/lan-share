package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareLinkBypassesSecureModeOnlyForScopedFile(t *testing.T) {
	a := newTestApp(t)
	if err := a.setSecureMode(true); err != nil {
		t.Fatal(err)
	}
	want := []byte("temporary-share-content")
	if err := os.WriteFile(filepath.Join(a.ShareDir(), "ticket.txt"), want, 0644); err != nil {
		t.Fatal(err)
	}

	req := localReq(http.MethodPost, "/api/share-links", strings.NewReader(`{"name":"ticket.txt","ttl_seconds":600}`))
	w := httptest.NewRecorder()
	a.handleShareLinks(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create share link: %d %s", w.Code, w.Body.String())
	}
	var link ShareLink
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Token == "" || link.URL == "" {
		t.Fatalf("bad share link: %+v", link)
	}

	publicReq := httptest.NewRequest(http.MethodGet, "/s/"+link.Token, nil)
	publicReq.RemoteAddr = "192.0.2.50:5000"
	publicW := httptest.NewRecorder()
	a.handlePublicShare(publicW, publicReq)
	if publicW.Code != http.StatusOK || !bytes.Equal(publicW.Body.Bytes(), want) {
		t.Fatalf("public share failed: status=%d body=%q", publicW.Code, publicW.Body.Bytes())
	}

	normalReq := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	normalReq.RemoteAddr = "192.0.2.50:5000"
	normalW := httptest.NewRecorder()
	a.handleFiles(normalW, normalReq)
	if normalW.Code != http.StatusUnauthorized {
		t.Fatalf("normal secure listing should remain protected: %d", normalW.Code)
	}
}

func TestShareLinkQRPNG(t *testing.T) {
	a := newTestApp(t)
	if err := os.WriteFile(filepath.Join(a.ShareDir(), "qr.bin"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	req := localReq(http.MethodPost, "/api/share-links", strings.NewReader(`{"name":"qr.bin","ttl_seconds":600}`))
	w := httptest.NewRecorder()
	a.handleShareLinks(w, req)
	var link ShareLink
	_ = json.Unmarshal(w.Body.Bytes(), &link)

	qrReq := localReq(http.MethodGet, "/api/share-links/qr?token="+link.Token, nil)
	qrW := httptest.NewRecorder()
	a.handleShareLinkQR(qrW, qrReq)
	if qrW.Code != http.StatusOK || qrW.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("qr failed: %d %s", qrW.Code, qrW.Header().Get("Content-Type"))
	}
	b, _ := io.ReadAll(qrW.Result().Body)
	if !bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("QR response is not PNG")
	}
}

func TestShareLinkKeepsOriginalFileAfterShareDirSwitch(t *testing.T) {
	a := newTestApp(t)
	oldDir := a.ShareDir()
	if err := os.WriteFile(filepath.Join(oldDir, "stable.txt"), []byte("from-old-dir"), 0644); err != nil {
		t.Fatal(err)
	}

	req := localReq(http.MethodPost, "/api/share-links", strings.NewReader(`{"name":"stable.txt","ttl_seconds":600}`))
	w := httptest.NewRecorder()
	a.handleShareLinks(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create share link: %d %s", w.Code, w.Body.String())
	}
	var link ShareLink
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}

	newDir := t.TempDir()
	if err := a.setShareDir(newDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "stable.txt"), []byte("from-new-dir"), 0644); err != nil {
		t.Fatal(err)
	}

	publicReq := httptest.NewRequest(http.MethodGet, "/s/"+link.Token, nil)
	publicReq.RemoteAddr = "192.0.2.60:5000"
	publicW := httptest.NewRecorder()
	a.handlePublicShare(publicW, publicReq)
	if publicW.Code != http.StatusOK {
		t.Fatalf("public share failed: %d %s", publicW.Code, publicW.Body.String())
	}
	if got := publicW.Body.String(); got != "from-old-dir" {
		t.Fatalf("share link changed target after directory switch: %q", got)
	}
}
