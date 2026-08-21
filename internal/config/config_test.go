package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPorts(t *testing.T) {
	if DefaultHTTPPort != 18888 {
		t.Fatalf("DefaultHTTPPort=%d want=18888", DefaultHTTPPort)
	}
	if DefaultDiscoveryPort != 51889 {
		t.Fatalf("DefaultDiscoveryPort=%d want=51889", DefaultDiscoveryPort)
	}
}

func TestPrepareShareDirCreatesDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "share")
	got, err := PrepareShareDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("got=%q want=%q", got, p)
	}
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		t.Fatalf("directory missing: %v", err)
	}
}

func TestSaveLoadShareDir(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "LANShare", "config.json")
	want := filepath.Join(t.TempDir(), "share")
	if err := SaveShareDir(cfg, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadShareDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}
