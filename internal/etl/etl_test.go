package etl

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

const (
	gamesFixture = "../../testdata/club_night_games.json"
	logsFixture  = "../../testdata/command_logs.json"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC) }

func newLoader(t *testing.T) (Loader, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "etl.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return Loader{Store: st, Now: fixedNow}, path
}

func TestParseGamesKeepsOnlyAggregateFields(t *testing.T) {
	input := `{"games": [
		{"id": "g1", "club_night_date": "2026-09-02", "reported_at": "2026-09-02T21:00:00+00:00",
		 "result": "side1", "points_size": 3000,
		 "players": [
			{"user_id": "111", "display_name": "Alice", "faction": " Ultramarines ", "side": "side1", "result": "win"},
			{"user_id": "222", "display_name": "Bob", "faction": "", "side": "side2", "result": "loss"}
		 ]},
		{"id": "bad-result", "club_night_date": "2026-09-02", "reported_at": "2026-09-02T21:00:00+00:00", "result": "side3"},
		{"id": "bad-date", "club_night_date": "soon", "reported_at": "2026-09-02T21:00:00+00:00", "result": "draw"}
	]}`
	games, read, err := ParseGames(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if read != 3 || len(games) != 1 {
		t.Fatalf("read=%d kept=%d, want 3/1", read, len(games))
	}
	g := games[0]
	if g.Game != "heresy" {
		t.Errorf("legacy game without key should default to heresy, got %q", g.Game)
	}
	if len(g.Factions) != 1 || g.Factions[0].Faction != "Ultramarines" {
		t.Errorf("factions = %+v, want trimmed Ultramarines only", g.Factions)
	}
}

func TestParseCommandLogsExtractsCompletionsOnly(t *testing.T) {
	input := `[
		{"insertId": "a", "timestamp": "2026-09-01T19:00:00.123Z",
		 "jsonPayload": {"message": "/club-night signup done guild=1 channel=2 user=3 success=True elapsed_ms=180"}},
		{"insertId": "b", "timestamp": "2026-09-01T19:00:00Z",
		 "jsonPayload": {"message": "/ping invoked guild=1 channel=2 user=3"}},
		{"timestamp": "2026-09-01T19:01:00Z",
		 "jsonPayload": {"message": "/ping done guild=None channel=None user=3 success=False elapsed_ms=12"}},
		{"insertId": "c", "timestamp": "2026-09-01T19:02:00Z",
		 "jsonPayload": {"message": "Scheduler tick complete"}}
	]`
	rows, read, err := ParseCommandLogs(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if read != 4 || len(rows) != 2 {
		t.Fatalf("read=%d kept=%d, want 4/2", read, len(rows))
	}
	if rows[0].Command != "club-night signup" || !rows[0].Success || rows[0].ElapsedMS != 180 || rows[0].SourceKey != "log:a" {
		t.Errorf("unexpected first row: %+v", rows[0])
	}
	if rows[1].Success || !strings.HasPrefix(rows[1].SourceKey, "sha256:") {
		t.Errorf("unexpected second row: %+v", rows[1])
	}
}

func TestLoadFixturesIsIdempotentAndAudited(t *testing.T) {
	loader, _ := newLoader(t)
	ctx := context.Background()

	first, err := loader.LoadCommandLogs(ctx, logsFixture)
	if err != nil {
		t.Fatal(err)
	}
	if first.RowsRead != 600 || first.RowsLoaded != 300 {
		t.Errorf("first load read/loaded = %d/%d, want 600/300", first.RowsRead, first.RowsLoaded)
	}
	second, err := loader.LoadCommandLogs(ctx, logsFixture)
	if err != nil {
		t.Fatal(err)
	}
	if second.RowsLoaded != 0 {
		t.Errorf("reload loaded %d rows, want 0", second.RowsLoaded)
	}

	games, err := loader.LoadGames(ctx, gamesFixture)
	if err != nil || games.RowsLoaded != 40 {
		t.Fatalf("games load: %+v err=%v", games, err)
	}

	runs, err := loader.Store.LatestRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Status != "ok" || runs[1].Status != "ok" {
		t.Errorf("unexpected audit trail: %+v", runs)
	}
}

func TestFailedLoadIsRecorded(t *testing.T) {
	loader, _ := newLoader(t)
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := loader.LoadGames(ctx, missing); err == nil {
		t.Fatal("expected error for missing file")
	}
	runs, err := loader.Store.LatestRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].Error == "" {
		t.Errorf("failure not audited: %+v", runs)
	}
}

// TestMemberDataNeverReachesDatabase scans every text value in every table
// for identifiers and free text present in the source fixtures.
func TestMemberDataNeverReachesDatabase(t *testing.T) {
	loader, dbPath := newLoader(t)
	ctx := context.Background()
	if _, err := loader.LoadGames(ctx, gamesFixture); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.LoadCommandLogs(ctx, logsFixture); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	forbidden := []string{"Synthetic Player", "900000000000000", "100000000000000001",
		"300000000000000001", "SYNTHETIC-PRIVATE-NOTE", "Organised Play", "Take and Hold"}
	for _, table := range []string{"command_invocations", "games", "game_factions", "etl_runs"} {
		dump := dumpTable(t, db, table)
		for _, needle := range forbidden {
			if strings.Contains(dump, needle) {
				t.Errorf("table %s contains member data %q", table, needle)
			}
		}
	}
}

func dumpTable(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			b.WriteString(v.String)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestMain(m *testing.M) {
	if _, err := os.Stat(gamesFixture); err != nil {
		panic("fixtures missing; run tests from the module root with go test ./...")
	}
	os.Exit(m.Run())
}
