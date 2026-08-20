//go:build !windows

package platform

// StartTray is intentionally a no-op on non-Windows CGO-free builds.
// macOS/Linux keep the lightweight browser UI; a native tray can be added as an optional shell later.
func StartTray(localURL string) (func(), error) {
	return func() {}, nil
}
