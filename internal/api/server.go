// Package api serves the public JSON API, the HTML dashboard, health probes
// and Prometheus metrics.
package api

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	apispec "github.com/blindfaithwargaming/bfwg-metrics/api"
	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

//go:embed web
var webFS embed.FS

const (
	defaultUsageDays = 30
	maxUsageDays     = 365
)

type Config struct {
	Store  *store.Store
	Logger *slog.Logger
	// Registry receives HTTP and ETL metrics; a fresh registry per server
	// keeps tests independent of the global default.
	Registry *prometheus.Registry
	// MinFactionGames withholds rarely played factions from public output.
	MinFactionGames int
	Now             func() time.Time
}

type server struct {
	Config
	dashboard *template.Template
}

func New(cfg Config) (http.Handler, error) {
	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"pct": func(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + "%" },
	}).ParseFS(webFS, "web/index.html")
	if err != nil {
		return nil, err
	}
	s := &server{Config: cfg, dashboard: tmpl}
	cfg.Registry.MustRegister(newETLCollector(cfg.Store, cfg.Logger))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /api/openapi.yaml", s.handleOpenAPI)
	mux.HandleFunc("GET /api/v1/commands/usage", s.handleCommandUsage)
	mux.HandleFunc("GET /api/v1/games/summary", s.handleGamesSummary)
	mux.HandleFunc("GET /api/v1/etl/runs", s.handleETLRuns)

	return instrument(mux, cfg.Registry, cfg.Logger), nil
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("ok\n"))
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		s.Logger.Warn("readiness check failed", "err", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

func (s *server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(apispec.OpenAPI)
}

func (s *server) handleCommandUsage(w http.ResponseWriter, r *http.Request) {
	days, err := parseDays(r.URL.Query().Get("days"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	usage, err := s.Store.CommandUsage(r.Context(), s.Now().AddDate(0, 0, -days))
	if err != nil {
		s.serverError(w, "command usage query", err)
		return
	}
	writeJSON(w, map[string]any{"days": days, "commands": nonNil(usage)})
}

func (s *server) handleGamesSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.Store.GamesSummary(r.Context(), s.MinFactionGames)
	if err != nil {
		s.serverError(w, "games summary query", err)
		return
	}
	writeJSON(w, summary)
}

func (s *server) handleETLRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.LatestRuns(r.Context())
	if err != nil {
		s.serverError(w, "etl runs query", err)
		return
	}
	writeJSON(w, map[string]any{"runs": nonNil(runs)})
}

type dashboardData struct {
	Days     int
	Usage    []store.CommandUsage
	Games    store.GamesSummary
	Runs     []store.Run
	MinGames int
}

func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := dashboardData{Days: defaultUsageDays, MinGames: s.MinFactionGames}
	var err error
	if data.Usage, err = s.Store.CommandUsage(ctx, s.Now().AddDate(0, 0, -defaultUsageDays)); err != nil {
		s.serverError(w, "dashboard usage", err)
		return
	}
	if data.Games, err = s.Store.GamesSummary(ctx, s.MinFactionGames); err != nil {
		s.serverError(w, "dashboard games", err)
		return
	}
	if data.Runs, err = s.Store.LatestRuns(ctx); err != nil {
		s.serverError(w, "dashboard runs", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.dashboard.Execute(w, data); err != nil {
		s.Logger.Error("render dashboard", "err", err)
	}
}

func parseDays(raw string) (int, error) {
	if raw == "" {
		return defaultUsageDays, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > maxUsageDays {
		return 0, errBadDays
	}
	return days, nil
}

type apiError string

func (e apiError) Error() string { return string(e) }

const errBadDays = apiError("days must be an integer between 1 and 365")

func (s *server) serverError(w http.ResponseWriter, what string, err error) {
	s.Logger.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// nonNil makes empty results encode as [] rather than null, as the spec promises.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
