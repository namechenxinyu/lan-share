package app

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/namechenxinyu/lan-share/internal/discovery"
	"github.com/namechenxinyu/lan-share/internal/fileutil"
	"github.com/namechenxinyu/lan-share/internal/qrcode"
)

type ShareLink struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	URL       string `json:"url"`
	Path      string `json:"-"`
}

func (a *App) handleShareLinks(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, "local management only", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.listShareLinks())
	case http.MethodPost:
		var body struct {
			Name       string `json:"name"`
			TTLSeconds int64  `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		name, err := fileutil.CleanFileName(body.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.TTLSeconds == 0 {
			body.TTLSeconds = 10 * 60
		}
		if body.TTLSeconds < 60 || body.TTLSeconds > 24*60*60 {
			http.Error(w, "ttl_seconds must be between 60 and 86400", http.StatusBadRequest)
			return
		}
		path := filepath.Join(a.ShareDir(), name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		now := time.Now()
		link := ShareLink{
			Token:     randomID() + randomID()[:8],
			Name:      name,
			Size:      info.Size(),
			CreatedAt: now.Unix(),
			ExpiresAt: now.Add(time.Duration(body.TTLSeconds) * time.Second).Unix(),
			Path:      path,
		}
		link.URL = a.shareLinkURL(link.Token)
		a.shareMu.Lock()
		a.cleanupShareLinksLocked(now)
		a.shareLinks[link.Token] = link
		a.shareMu.Unlock()
		writeJSON(w, http.StatusCreated, link)
	case http.MethodDelete:
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		a.shareMu.Lock()
		delete(a.shareLinks, token)
		a.shareMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, "GET, POST, DELETE")
	}
}

func (a *App) handleShareLinkQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", http.StatusForbidden)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	link, ok := a.getShareLink(token)
	if !ok {
		http.Error(w, "share link not found or expired", http.StatusNotFound)
		return
	}
	img, err := qrcode.EncodePNG(link.URL, 7)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img)
}

func (a *App) handlePublicShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/s/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	link, ok := a.getShareLink(token)
	if !ok {
		http.Error(w, "share link expired or revoked", http.StatusGone)
		return
	}
	path := link.Path
	if path == "" {
		path = filepath.Join(a.ShareDir(), link.Name)
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(link.Name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", fileutil.ContentDisposition(link.Name))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-LAN-Share-Expires", fmt.Sprintf("%d", link.ExpiresAt))
	http.ServeContent(w, r, link.Name, info.ModTime(), f)
}

func (a *App) shareLinkURL(token string) string {
	host := "127.0.0.1"
	if ips := discovery.LocalIPv4s(); len(ips) > 0 {
		host = ips[0]
	}
	return fmt.Sprintf("http://%s:%d/s/%s", host, a.cfg.Port, token)
}

func (a *App) getShareLink(token string) (ShareLink, bool) {
	now := time.Now()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	a.cleanupShareLinksLocked(now)
	link, ok := a.shareLinks[token]
	if !ok {
		return ShareLink{}, false
	}
	// Refresh the LAN IP if network interfaces changed since creation.
	link.URL = a.shareLinkURL(link.Token)
	a.shareLinks[token] = link
	return link, true
}

func (a *App) listShareLinks() []ShareLink {
	now := time.Now()
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	a.cleanupShareLinksLocked(now)
	out := make([]ShareLink, 0, len(a.shareLinks))
	for token, link := range a.shareLinks {
		link.URL = a.shareLinkURL(token)
		a.shareLinks[token] = link
		out = append(out, link)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt < out[j].ExpiresAt })
	return out
}

func (a *App) cleanupShareLinksLocked(now time.Time) {
	ts := now.Unix()
	for token, link := range a.shareLinks {
		if link.ExpiresAt <= ts {
			delete(a.shareLinks, token)
		}
	}
}
