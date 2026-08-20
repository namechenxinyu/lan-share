package platform

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const startupName = "LANShare"

func AutoStartEnabled() bool {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", startupName).Run() == nil
	case "darwin":
		home, _ := os.UserHomeDir()
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.lanshare.app.plist"))
		return err == nil
	default:
		home, _ := os.UserHomeDir()
		_, err := os.Stat(filepath.Join(home, ".config", "autostart", "lan-share.desktop"))
		return err == nil
	}
}

func SetAutoStart(enabled bool, executable string) error {
	if strings.TrimSpace(executable) == "" {
		return errors.New("empty executable path")
	}
	if enabled {
		return enableAutoStart(executable)
	}
	return disableAutoStart()
}

func enableAutoStart(executable string) error {
	switch runtime.GOOS {
	case "windows":
		value := strconv.Quote(executable) + " -open=false"
		return exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", startupName, "/t", "REG_SZ", "/d", value, "/f").Run()
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, "Library", "LaunchAgents")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		path := filepath.Join(dir, "com.lanshare.app.plist")
		exe := xmlEscape(executable)
		body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.lanshare.app</string>
<key>ProgramArguments</key><array><string>` + exe + `</string><string>-open=false</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><false/>
</dict></plist>
`
		return os.WriteFile(path, []byte(body), 0644)
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".config", "autostart")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		path := filepath.Join(dir, "lan-share.desktop")
		body := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=LAN Share\nExec=%s -open=false\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", shellQuoteDesktop(executable))
		return os.WriteFile(path, []byte(body), 0644)
	}
}

func disableAutoStart() error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", startupName, "/f")
		if err := cmd.Run(); err != nil && AutoStartEnabled() {
			return err
		}
		return nil
	case "darwin":
		home, _ := os.UserHomeDir()
		err := os.Remove(filepath.Join(home, "Library", "LaunchAgents", "com.lanshare.app.plist"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	default:
		home, _ := os.UserHomeDir()
		err := os.Remove(filepath.Join(home, ".config", "autostart", "lan-share.desktop"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func shellQuoteDesktop(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
