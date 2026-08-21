package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func testServerPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	_, rawPort, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	return port
}

func TestProbeLANShare(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"version":"0.9.2"}`)
	}))
	defer srv.Close()

	version, ok := probeLANShare(testServerPort(t, srv))
	if !ok {
		t.Fatal("expected LAN Share probe to succeed")
	}
	if version != "0.9.2" {
		t.Fatalf("unexpected version: %q", version)
	}
}

func TestProbeLANShareRejectsOtherService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not LAN Share")
	}))
	defer srv.Close()

	if version, ok := probeLANShare(testServerPort(t, srv)); ok || version != "" {
		t.Fatalf("unexpected successful probe: version=%q ok=%v", version, ok)
	}
}

func TestLocalURL(t *testing.T) {
	if got := localURL(18888); got != "http://127.0.0.1:18888" {
		t.Fatalf("unexpected local URL: %s", got)
	}
}
