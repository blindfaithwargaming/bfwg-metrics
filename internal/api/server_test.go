package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/etl"
	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

func newTestServer(t *testing.T, loadFixtures bool) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if loadFixtures {
		loader := etl.Loader{Store: st, Now: fixedNow}
		if _, err := loader.LoadGames(ctx, "../../testdata/club_night_games.json"); err != nil {
			t.Fatal(err)
		}
		if _, err := loader.LoadCommandLogs(ctx, "../../testdata/command_logs.json"); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Config{
		Store:           st,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registry:        prometheus.NewRegistry(),
		MinFactionGames: 3,
		Now:             fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestCommandUsageEndpoint(t *testing.T) {
	srv := newTestServer(t, true)
	code, body := get(t, srv, "/api/v1/commands/usage?days=60")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var resp struct {
		Days     int                  `json:"days"`
		Commands []store.CommandUsage `json:"commands"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, c := range resp.Commands {
		total += c.Invocations
	}
	if resp.Days != 60 || total != 300 {
		t.Errorf("days=%d total=%d, want 60/300", resp.Days, total)
	}
}

func TestCommandUsageRejectsBadDays(t *testing.T) {
	srv := newTestServer(t, false)
	for _, q := range []string{"0", "366", "abc"} {
		code, body := get(t, srv, "/api/v1/commands/usage?days="+q)
		if code != http.StatusBadRequest || !strings.Contains(body, "days must be") {
			t.Errorf("days=%s: status %d body %s", q, code, body)
		}
	}
}

func TestEmptyDatabaseReturnsEmptyArrays(t *testing.T) {
	srv := newTestServer(t, false)
	for path, want := range map[string]string{
		"/api/v1/commands/usage": `"commands":[]`,
		"/api/v1/games/summary":  `"systems":[]`,
		"/api/v1/etl/runs":       `"runs":[]`,
	} {
		code, body := get(t, srv, path)
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("%s: status %d body %s", path, code, body)
		}
	}
}

func TestPublicOutputContainsNoMemberData(t *testing.T) {
	srv := newTestServer(t, true)
	forbidden := []string{"Synthetic Player", "900000000000000", "SYNTHETIC-PRIVATE-NOTE", "Organised Play"}
	for _, path := range []string{"/", "/api/v1/games/summary", "/api/v1/commands/usage", "/api/v1/etl/runs"} {
		code, body := get(t, srv, path)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", path, code)
		}
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("%s exposes %q", path, needle)
			}
		}
	}
}

func TestDashboardRendersLoadedData(t *testing.T) {
	srv := newTestServer(t, true)
	code, body := get(t, srv, "/")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"/club-night signup", "heresy", "command_logs"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestProbesAndSpec(t *testing.T) {
	srv := newTestServer(t, false)
	for path, want := range map[string]string{
		"/healthz":          "ok",
		"/readyz":           "ok",
		"/api/openapi.yaml": "openapi: 3.0.3",
	} {
		code, body := get(t, srv, path)
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("%s: status %d body %.80s", path, code, body)
		}
	}
	if code, _ := get(t, srv, "/nope"); code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", code)
	}
}

func TestMetricsExposeHTTPAndETLSeries(t *testing.T) {
	srv := newTestServer(t, true)
	get(t, srv, "/api/v1/games/summary")
	_, body := get(t, srv, "/metrics")
	for _, want := range []string{
		`bfwg_http_requests_total{code="200",method="GET",route="GET /api/v1/games/summary"} 1`,
		`bfwg_etl_last_run_success{source="games"} 1`,
		`bfwg_etl_last_run_rows_loaded{source="command_logs"} 300`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
