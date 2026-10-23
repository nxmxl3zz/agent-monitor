package monitorweb

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

//go:embed assets/*
var assets embed.FS

type Loader func(context.Context, core.TimeWindow) (View, error)

type Server struct {
	addr          string
	hosts         map[string]bool
	defaultWindow core.TimeWindow
	load          Loader
	dashboard     func(context.Context, string) (Dashboard, error)
}

func (s *Server) SetDashboardLoader(load func(context.Context, string) (Dashboard, error)) {
	s.dashboard = load
}

func NewServer(addr string, allowedHosts []string, window core.TimeWindow, load Loader) (*Server, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("monitor web: invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("monitor web: listen on a loopback IP; use an authenticated proxy for remote access")
	}
	s := &Server{addr: addr, hosts: map[string]bool{strings.ToLower(addr): true, "localhost:" + port: true}, defaultWindow: window, load: load}
	for _, authority := range allowedHosts {
		parsed, err := url.Parse("//" + authority)
		if err != nil || parsed.Host == "" || parsed.Host != authority || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(authority, "* \\") {
			return nil, fmt.Errorf("monitor web: --allow-host needs a hostname with optional port")
		}
		s.hosts[strings.ToLower(authority)] = true
	}
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if !s.hosts[strings.ToLower(r.Host)] {
		http.Error(w, "Host not allowed", http.StatusForbidden)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !strings.EqualFold(parsed.Host, r.Host) || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Read only", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/api/dashboard" {
		period := r.URL.Query().Get("range")
		if core.MonitorRangeDuration(period) == 0 {
			http.Error(w, "Invalid period", http.StatusBadRequest)
			return
		}
		if s.dashboard == nil {
			http.Error(w, "Charts unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		view, err := s.dashboard(ctx, period)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Charts temporarily unavailable"})
			}
			return
		}
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(view)
		}
		return
	}
	if r.URL.Path == "/api/snapshot" {
		window := s.defaultWindow
		if raw := r.URL.Query().Get("window"); raw != "" {
			window = core.ParseTimeWindow(raw)
			if string(window) != raw {
				http.Error(w, "Invalid period", http.StatusBadRequest)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		view, err := s.load(ctx, window)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Collector unavailable"})
			}
			return
		}
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(view)
		}
		return
	}
	file, contentType := "", ""
	switch r.URL.Path {
	case "/":
		file, contentType = "index.html", "text/html; charset=utf-8"
	case "/app.css":
		file, contentType = "app.css", "text/css; charset=utf-8"
	case "/app.js":
		file, contentType = "app.js", "text/javascript; charset=utf-8"
	case "/charts.js":
		file, contentType = "charts.js", "text/javascript; charset=utf-8"
	case "/inter.woff2":
		file, contentType = "inter.woff2", "font/woff2"
	case "/favicon.svg":
		file, contentType = "favicon.svg", "image/svg+xml"
	default:
		http.NotFound(w, r)
		return
	}
	body, err := assets.ReadFile("assets/" + file)
	if err != nil {
		http.Error(w, "Resource unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("monitor web: listen: %w", err)
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
