package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed static
var staticFiles embed.FS

type config struct {
	addr        string
	dockerHost  string
	publicHost  string
	siteTitle   string
	siteTagline string
	showStopped bool
	cacheTTL    time.Duration
}

func loadConfig() config {
	return config{
		addr:        env("LISTEN_ADDR", ":8080"),
		dockerHost:  env("DOCKER_HOST", "unix:///var/run/docker.sock"),
		publicHost:  env("PUBLIC_HOST", ""),
		siteTitle:   env("SITE_TITLE", "The Salt Works"),
		siteTagline: env("SITE_TAGLINE", "Services running on this box"),
		showStopped: isTrue(env("SHOW_STOPPED", "false")),
		cacheTTL:    envDuration("CACHE_TTL", 10*time.Second),
	}
}

// snapshot is the payload served to the browser.
type snapshot struct {
	Title   string    `json:"title"`
	Tagline string    `json:"tagline"`
	Groups  []group   `json:"groups"`
	Updated time.Time `json:"updated"`
}

// registry polls Docker on demand and caches the result, so a burst of page
// loads (or a refresh loop across many tabs) doesn't hammer the socket.
type registry struct {
	docker *dockerClient
	cfg    config

	mu     sync.Mutex
	cached snapshot
	fresh  bool
	at     time.Time
}

func (r *registry) snapshot(ctx context.Context) (snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fresh && time.Since(r.at) < r.cfg.cacheTTL {
		return r.cached, nil
	}

	containers, err := r.docker.containers(ctx, r.cfg.showStopped)
	if err != nil {
		// Serve the last good result if we have one: a transient socket blip
		// shouldn't blank the dashboard.
		if r.fresh {
			return r.cached, nil
		}
		return snapshot{}, err
	}

	r.cached = snapshot{
		Title:   r.cfg.siteTitle,
		Tagline: r.cfg.siteTagline,
		Groups:  buildServices(containers, r.cfg.publicHost),
		Updated: time.Now().UTC(),
	}
	r.fresh = true
	r.at = time.Now()
	return r.cached, nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	cfg := loadConfig()

	docker, err := newDockerClient(cfg.dockerHost)
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}

	// Fail loudly at startup rather than serving an empty page forever if the
	// socket path or proxy permissions are wrong.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	if err := docker.ping(pingCtx); err != nil {
		log.Printf("warning: cannot reach docker at %s: %v", cfg.dockerHost, err)
	}
	cancelPing()

	reg := &registry{docker: docker, cfg: cfg}

	assets, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/api/services", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()

		snap, err := reg.snapshot(ctx)
		if err != nil {
			log.Printf("snapshot failed: %v", err)
			http.Error(w, `{"error":"docker unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(snap); err != nil {
			log.Printf("encode failed: %v", err)
		}
	})
	mux.Handle("/", http.FileServer(http.FS(assets)))

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("listening on %s (docker: %s)", cfg.addr, cfg.dockerHost)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	// Bare numbers are read as seconds, which is what most people type.
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Second
	}
	return fallback
}
