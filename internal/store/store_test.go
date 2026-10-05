package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var base = time.Date(2026, 9, 1, 19, 0, 0, 0, time.UTC)

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestInsertInvocationsDeduplicatesOnSourceKey(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	rows := []Invocation{
		{SourceKey: "a", OccurredAt: base, Command: "ping", Success: true, ElapsedMS: 5},
		{SourceKey: "b", OccurredAt: base, Command: "ping", Success: true, ElapsedMS: 7},
	}
	if n, err := s.InsertInvocations(ctx, rows); err != nil || n != 2 {
		t.Fatalf("first load: n=%d err=%v", n, err)
	}
	if n, err := s.InsertInvocations(ctx, rows); err != nil || n != 0 {
		t.Fatalf("reload should add nothing: n=%d err=%v", n, err)
	}
}

func TestCommandUsageComputesRatesAndNearestRankP95(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var rows []Invocation
	for i := 1; i <= 20; i++ {
		rows = append(rows, Invocation{
			SourceKey: "lore" + string(rune('a'+i)), OccurredAt: base,
			Command: "lore random", Success: i != 20, ElapsedMS: i * 10,
		})
	}
	rows = append(rows, Invocation{SourceKey: "old", OccurredAt: base.AddDate(0, 0, -40),
		Command: "lore random", Success: false, ElapsedMS: 9999})
	if _, err := s.InsertInvocations(ctx, rows); err != nil {
		t.Fatal(err)
	}

	usage, err := s.CommandUsage(ctx, base.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("want 1 command, got %+v", usage)
	}
	u := usage[0]
	if u.Invocations != 20 || u.Failures != 1 {
		t.Errorf("invocations/failures = %d/%d, want 20/1", u.Invocations, u.Failures)
	}
	if u.SuccessRate != 0.95 {
		t.Errorf("success rate = %v, want 0.95", u.SuccessRate)
	}
	// Nearest rank: ceil(0.95 * 20) = 19th value = 190ms.
	if u.P95MS != 190 {
		t.Errorf("p95 = %d, want 190", u.P95MS)
	}
	if u.AvgMS != 105 {
		t.Errorf("avg = %v, want 105", u.AvgMS)
	}
}

func game(id, system, result string, factions ...GameFaction) Game {
	return Game{ID: id, ClubNightDate: "2026-09-01", ReportedAt: base,
		Game: system, Result: result, Factions: factions}
}

func TestReplaceGamesRemovesGamesDeletedUpstream(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	fa := GameFaction{Side: "side1", Faction: "Ultramarines", Outcome: "win"}
	if _, err := s.ReplaceGames(ctx, []Game{game("g1", "heresy", "side1", fa), game("g2", "heresy", "side1", fa)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceGames(ctx, []Game{game("g1", "heresy", "side1", fa)}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.GamesSummary(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Systems[0].Games != 1 || summary.Factions[0].Played != 1 {
		t.Errorf("stale rows survived replace: %+v", summary)
	}
}

func TestGamesSummaryWithholdsRareFactions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	common := func(outcome string) GameFaction {
		return GameFaction{Side: "side1", Faction: "Death Guard", Outcome: outcome}
	}
	rare := GameFaction{Side: "side2", Faction: "Blackshields", Outcome: "loss"}
	games := []Game{
		game("g1", "heresy", "side1", common("win"), rare),
		game("g2", "heresy", "side2", common("loss")),
		game("g3", "heresy", "draw", common("draw")),
	}
	if _, err := s.ReplaceGames(ctx, games); err != nil {
		t.Fatal(err)
	}
	summary, err := s.GamesSummary(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Factions) != 1 || summary.Factions[0].Faction != "Death Guard" {
		t.Fatalf("want only Death Guard, got %+v", summary.Factions)
	}
	f := summary.Factions[0]
	if f.Wins != 1 || f.Losses != 1 || f.Draws != 1 {
		t.Errorf("record = %d/%d/%d, want 1/1/1", f.Wins, f.Losses, f.Draws)
	}
	if summary.Systems[0].Draws != 1 {
		t.Errorf("draws = %d, want 1", summary.Systems[0].Draws)
	}
}

func TestLatestRunsReturnsNewestPerSource(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i, status := range []string{"ok", "failed"} {
		if err := s.RecordRun(ctx, Run{Source: "games", Status: status,
			StartedAt: base.Add(time.Duration(i) * time.Hour), FinishedAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.LatestRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || !runs[0].FinishedAt.Equal(base.Add(time.Hour)) {
		t.Errorf("unexpected runs: %+v", runs)
	}
}
