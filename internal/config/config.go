package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DefaultHTTPPort      = 18888
	DefaultDiscoveryPort = 51889
)

type Runtime struct {
	Port          int
	DiscoveryPort int
	ShareDir      string
	Name          string
	AutoOpen      bool
	SyncWrites    bool
	SecureMode    bool
	Tray          bool
	ConfigPath    string
	SecurityPath  string
}

type Settings struct {
	ShareDir   string `json:"share_dir"`
	Name       string `json:"name,omitempty"`
	SecureMode bool   `json:"secure_mode,omitempty"`
}

func Parse(args []string) (Runtime, error) {
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, "LANShare")
	host, _ := os.Hostname()
	configPath := defaultConfigPath(home)
	securityPath := filepath.Join(filepath.Dir(configPath), "security.json")

	stored, _ := LoadSettings(configPath)
	if strings.TrimSpace(stored.ShareDir) == "" {
		stored.ShareDir = defaultDir
	}
	if strings.TrimSpace(stored.Name) == "" {
		stored.Name = host
	}

	fs := flag.NewFlagSet("lan-share", flag.ContinueOnError)
	var cfg Runtime
	fs.IntVar(&cfg.Port, "port", DefaultHTTPPort, "HTTP port")
	fs.IntVar(&cfg.DiscoveryPort, "discovery-port", DefaultDiscoveryPort, "UDP discovery port")
	fs.StringVar(&cfg.ShareDir, "dir", stored.ShareDir, "shared/received files directory")
	fs.StringVar(&cfg.Name, "name", stored.Name, "device display name")
	fs.BoolVar(&cfg.AutoOpen, "open", true, "open browser automatically")
	fs.BoolVar(&cfg.SyncWrites, "sync", false, "fsync uploads before reporting completion (slower)")
	fs.BoolVar(&cfg.SecureMode, "secure", stored.SecureMode, "require pairing for remote file access and encrypt paired agent transfers")
	fs.BoolVar(&cfg.Tray, "tray", runtime.GOOS == "windows", "show native Windows tray icon")
	if err := fs.Parse(args); err != nil {
		return Runtime{}, err
	}
	if cfg.Port < 1 || cfg.Port > 65535 || cfg.DiscoveryPort < 1 || cfg.DiscoveryPort > 65535 {
		return Runtime{}, errors.New("port must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "LAN-Share"
	}
	cfg.ConfigPath = configPath
	cfg.SecurityPath = securityPath

	dir, err := PrepareShareDir(cfg.ShareDir)
	if err != nil {
		return Runtime{}, fmt.Errorf("prepare share directory: %w", err)
	}
	cfg.ShareDir = dir
	return cfg, nil
}

func defaultConfigPath(home string) string {
	if d, err := os.UserConfigDir(); err == nil && strings.TrimSpace(d) != "" {
		return filepath.Join(d, "LANShare", "config.json")
	}
	return filepath.Join(home, ".lan-share.json")
}

func PrepareShareDir(raw string) (string, error) {
	p := strings.TrimSpace(os.ExpandEnv(raw))
	if p == "" {
		return "", errors.New("share directory is empty")
	}
	if strings.HasPrefix(p, "~"+string(os.PathSeparator)) || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	probe, err := os.CreateTemp(abs, ".lanshare-write-test-*")
	if err != nil {
		return "", fmt.Errorf("directory is not writable: %w", err)
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return filepath.Clean(abs), nil
}

func SaveSettings(configPath string, s Settings) error {
	if strings.TrimSpace(configPath) == "" {
		return errors.New("config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(configPath, b, 0600)
}

func LoadSettings(configPath string) (Settings, error) {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Compatibility helpers retained for older callers/tests.
func SaveShareDir(configPath, shareDir string) error {
	s, _ := LoadSettings(configPath)
	s.ShareDir = shareDir
	return SaveSettings(configPath, s)
}

func LoadShareDir(configPath string) (string, error) {
	s, err := LoadSettings(configPath)
	return s.ShareDir, err
}
