package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/namechenxinyu/lan-share/internal/app"
	"github.com/namechenxinyu/lan-share/internal/config"
	"github.com/namechenxinyu/lan-share/internal/platform"
)

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		if version, ok := probeLANShare(cfg.Port); ok {
			url := localURL(cfg.Port)
			log.Printf("LAN Share %s is already running at %s", version, url)
			if cfg.AutoOpen {
				_ = platform.OpenURL(url)
			}
			return
		}
		log.Fatalf("cannot listen on TCP port %d: %v\nThe port may be occupied or reserved by Windows. Try another port with -port <port>.", cfg.Port, err)
	}
	defer listener.Close()

	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	if cfg.Tray {
		stopTray, trayErr := platform.StartTray(a.LocalURL())
		if trayErr != nil {
			log.Printf("tray disabled: %v", trayErr)
		} else {
			defer stopTray()
		}
	}
	if err := a.StartDiscovery(); err != nil {
		log.Printf("UDP discovery disabled: %v", err)
	}

	srv := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Do not set whole-request ReadTimeout/WriteTimeout: very large transfers may run for hours.
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}

	log.Printf("LAN Share %s started", app.Version)
	log.Printf("Share directory: %s", a.ShareDir())
	log.Printf("Local UI: %s", a.LocalURL())
	for _, u := range a.LANURLs() {
		log.Printf("LAN UI: %s", u)
	}
	if cfg.AutoOpen {
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = platform.OpenURL(a.LocalURL())
		}()
	}

	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func localURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func probeLANShare(port int) (string, bool) {
	client := &http.Client{Timeout: 750 * time.Millisecond}
	resp, err := client.Get(localURL(port) + "/healthz")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var health healthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health); err != nil {
		return "", false
	}
	return health.Version, health.OK && health.Version != ""
}
