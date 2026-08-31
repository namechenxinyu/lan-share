//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitUploadClosesPartialFileBeforeFinalize(t *testing.T) {
	dir := t.TempDir()
	a := &App{shareDir: dir, sessions: make(map[string]*uploadSession)}

	cases := []struct {
		id   string
		name string
	}{
		{id: "windows-upload-001", name: "first.bin"},
		{id: "windows-upload-002", name: "second.bin"},
	}

	for _, tc := range cases {
		s, err := a.initUpload(uploadInitRequest{ID: tc.id, Name: tc.name, Size: 0}, "")
		if err != nil {
			t.Fatalf("initUpload(%s): %v", tc.id, err)
		}
		final := filepath.Join(dir, tc.name)
		if err := os.Rename(s.Path, final); err != nil {
			t.Fatalf("rename partial upload after init (%s): %v", tc.id, err)
		}
		a.deleteSession(tc.id)
		if _, err := os.Stat(final); err != nil {
			t.Fatalf("final file missing after rename (%s): %v", tc.id, err)
		}
	}
}
