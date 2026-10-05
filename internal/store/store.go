// Package store owns the SQLite schema and every SQL statement the service runs.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// TimeLayout is fixed-width so that stored timestamps sort lexicographically.
const TimeLayout = "2006-01-02T15:04:05.000Z"

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY between
	// our own goroutines while WAL still lets other processes read.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var applied int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name,
		).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().UTC().Format(TimeLayout),
		); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type Invocation struct {
	SourceKey  string
	OccurredAt time.Time
	Command    string
	Success    bool
	ElapsedMS  int
}

// InsertInvocations is idempotent on SourceKey, so overlapping log exports can
// be reloaded safely. It returns the number of new rows.
func (s *Store) InsertInvocations(ctx context.Context, rows []Invocation) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO command_invocations (source_key, occurred_at, command, success, elapsed_ms)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	loaded := 0
	for _, r := range rows {
		res, err := stmt.ExecContext(ctx, r.SourceKey, r.OccurredAt.UTC().Format(TimeLayout),
			r.Command, r.Success, r.ElapsedMS)
		if err != nil {
			return 0, fmt.Errorf("insert invocation %s: %w", r.SourceKey, err)
		}
		n, _ := res.RowsAffected()
		loaded += int(n)
	}
	return loaded, tx.Commit()
}

type Game struct {
	ID            string
	ClubNightDate string
	ReportedAt    time.Time
	Game          string
	Result        string
	PointsSize    *int
	Factions      []GameFaction
}

type GameFaction struct {
	Side    string
	Faction string
	Outcome string
}

// ReplaceGames swaps in a full snapshot. The bot deletes games via
// /club-night remove-game, so an append-only load would resurrect them.
func (s *Store) ReplaceGames(ctx context.Context, games []Game) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM games`); err != nil {
		return 0, err
	}
	gameStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO games (id, club_night_date, reported_at, game, result, points_size)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer gameStmt.Close()
	factionStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO game_factions (game_id, side, faction, outcome) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer factionStmt.Close()
	for _, g := range games {
		if _, err := gameStmt.ExecContext(ctx, g.ID, g.ClubNightDate,
			g.ReportedAt.UTC().Format(TimeLayout), g.Game, g.Result, g.PointsSize); err != nil {
			return 0, fmt.Errorf("insert game %s: %w", g.ID, err)
		}
		for _, f := range g.Factions {
			if _, err := factionStmt.ExecContext(ctx, g.ID, f.Side, f.Faction, f.Outcome); err != nil {
				return 0, fmt.Errorf("insert faction for game %s: %w", g.ID, err)
			}
		}
	}
	return len(games), tx.Commit()
}

type Run struct {
	Source     string    `json:"source"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	RowsRead   int       `json:"rows_read"`
	RowsLoaded int       `json:"rows_loaded"`
	Error      string    `json:"error,omitempty"`
}

func (s *Store) RecordRun(ctx context.Context, r Run) error {
	var errText sql.NullString
	if r.Error != "" {
		errText = sql.NullString{String: r.Error, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO etl_runs (source, started_at, finished_at, status, rows_read, rows_loaded, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Source, r.StartedAt.UTC().Format(TimeLayout), r.FinishedAt.UTC().Format(TimeLayout),
		r.Status, r.RowsRead, r.RowsLoaded, errText)
	return err
}

// LatestRuns returns the most recent run for each source.
func (s *Store) LatestRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, started_at, finished_at, status, rows_read, rows_loaded, COALESCE(error, '')
		FROM etl_runs
		WHERE id IN (SELECT MAX(id) FROM etl_runs GROUP BY source)
		ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var started, finished string
		if err := rows.Scan(&r.Source, &started, &finished, &r.Status,
			&r.RowsRead, &r.RowsLoaded, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(TimeLayout, started)
		r.FinishedAt, _ = time.Parse(TimeLayout, finished)
		out = append(out, r)
	}
	return out, rows.Err()
}

type CommandUsage struct {
	Command     string  `json:"command"`
	Invocations int     `json:"invocations"`
	Failures    int     `json:"failures"`
	SuccessRate float64 `json:"success_rate"`
	AvgMS       float64 `json:"avg_ms"`
	P95MS       int     `json:"p95_ms"`
}

// CommandUsage reports per-command volume, reliability and latency since the
// given time. p95 uses the nearest-rank method via a window function.
func (s *Store) CommandUsage(ctx context.Context, since time.Time) ([]CommandUsage, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH windowed AS (
			SELECT command, success, elapsed_ms,
			       ROW_NUMBER() OVER (PARTITION BY command ORDER BY elapsed_ms) AS rn,
			       COUNT(*)     OVER (PARTITION BY command)                     AS n
			FROM command_invocations
			WHERE occurred_at >= ?
		)
		SELECT command,
		       COUNT(*)                                         AS invocations,
		       SUM(1 - success)                                 AS failures,
		       AVG(elapsed_ms)                                  AS avg_ms,
		       MAX(CASE WHEN rn = (95 * n + 99) / 100 THEN elapsed_ms END) AS p95_ms
		FROM windowed
		GROUP BY command
		ORDER BY invocations DESC, command`,
		since.UTC().Format(TimeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommandUsage
	for rows.Next() {
		var u CommandUsage
		if err := rows.Scan(&u.Command, &u.Invocations, &u.Failures, &u.AvgMS, &u.P95MS); err != nil {
			return nil, err
		}
		u.SuccessRate = float64(u.Invocations-u.Failures) / float64(u.Invocations)
		out = append(out, u)
	}
	return out, rows.Err()
}

type GameSystemSummary struct {
	Game  string `json:"game"`
	Games int    `json:"games"`
	Draws int    `json:"draws"`
}

type FactionRecord struct {
	Game    string  `json:"game"`
	Faction string  `json:"faction"`
	Played  int     `json:"played"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	WinRate float64 `json:"win_rate"`
}

type GamesSummary struct {
	Systems  []GameSystemSummary `json:"systems"`
	Factions []FactionRecord     `json:"factions"`
}

// GamesSummary aggregates reported games. Factions played fewer than
// minFactionGames times are withheld: faction names are member free text and
// a one-off entry could identify who played it.
func (s *Store) GamesSummary(ctx context.Context, minFactionGames int) (GamesSummary, error) {
	out := GamesSummary{Systems: []GameSystemSummary{}, Factions: []FactionRecord{}}
	rows, err := s.db.QueryContext(ctx, `
		SELECT game, COUNT(*), SUM(result = 'draw')
		FROM games GROUP BY game ORDER BY COUNT(*) DESC, game`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var g GameSystemSummary
		if err := rows.Scan(&g.Game, &g.Games, &g.Draws); err != nil {
			rows.Close()
			return out, err
		}
		out.Systems = append(out.Systems, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT g.game, f.faction, COUNT(*),
		       SUM(f.outcome = 'win'), SUM(f.outcome = 'loss'), SUM(f.outcome = 'draw')
		FROM game_factions f JOIN games g ON g.id = f.game_id
		GROUP BY g.game, f.faction
		HAVING COUNT(*) >= ?
		ORDER BY COUNT(*) DESC, g.game, f.faction`, minFactionGames)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f FactionRecord
		if err := rows.Scan(&f.Game, &f.Faction, &f.Played, &f.Wins, &f.Losses, &f.Draws); err != nil {
			return out, err
		}
		f.WinRate = float64(f.Wins) / float64(f.Played)
		out.Factions = append(out.Factions, f)
	}
	return out, rows.Err()
}
