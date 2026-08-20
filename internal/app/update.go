package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (a *App) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/repos/namechenxinyu/lan-share/releases/latest", nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LAN-Share/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "update check failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		writeJSON(w, 200, map[string]any{"current": Version, "latest": "", "update_available": false})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		http.Error(w, fmt.Sprintf("GitHub HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))), 502)
		return
	}
	var v struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		http.Error(w, "invalid update response", 502)
		return
	}
	latest := strings.TrimPrefix(strings.TrimSpace(v.TagName), "v")
	writeJSON(w, 200, map[string]any{"current": Version, "latest": latest, "update_available": versionGreater(latest, Version), "url": v.HTMLURL, "name": v.Name})
}

func versionGreater(a, b string) bool {
	var av, bv [3]int
	fmt.Sscanf(a, "%d.%d.%d", &av[0], &av[1], &av[2])
	fmt.Sscanf(b, "%d.%d.%d", &bv[0], &bv[1], &bv[2])
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}
