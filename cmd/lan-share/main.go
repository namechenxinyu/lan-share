package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/namechenxinyu/lan-share/internal/app"
	"github.com/namechenxinyu/lan-share/internal/config"
	"github.com/namechenxinyu/lan-share/internal/platform"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	if err := a.StartDiscovery(); err != nil {
		log.Printf("UDP discovery disabled: %v", err)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
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

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
