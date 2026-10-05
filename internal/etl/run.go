package etl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

const (
	SourceGames       = "games"
	SourceCommandLogs = "command_logs"
)

// Clock is injectable so audit timestamps are deterministic in tests.
type Clock func() time.Time

type Loader struct {
	Store *store.Store
	Now   Clock
}

// LoadGames runs one extract-transform-load pass over a games file and
// records the outcome in etl_runs whether or not it succeeds.
func (l Loader) LoadGames(ctx context.Context, path string) (store.Run, error) {
	return l.run(ctx, SourceGames, func() (int, int, error) {
		f, err := os.Open(path)
		if err != nil {
			return 0, 0, err
		}
		defer f.Close()
		games, read, err := ParseGames(f)
		if err != nil {
			return read, 0, err
		}
		loaded, err := l.Store.ReplaceGames(ctx, games)
		return read, loaded, err
	})
}

func (l Loader) LoadCommandLogs(ctx context.Context, path string) (store.Run, error) {
	return l.run(ctx, SourceCommandLogs, func() (int, int, error) {
		f, err := os.Open(path)
		if err != nil {
			return 0, 0, err
		}
		defer f.Close()
		rows, read, err := ParseCommandLogs(f)
		if err != nil {
			return read, 0, err
		}
		loaded, err := l.Store.InsertInvocations(ctx, rows)
		return read, loaded, err
	})
}

func (l Loader) run(ctx context.Context, source string, fn func() (int, int, error)) (store.Run, error) {
	run := store.Run{Source: source, StartedAt: l.Now(), Status: "ok"}
	read, loaded, err := fn()
	run.FinishedAt = l.Now()
	run.RowsRead, run.RowsLoaded = read, loaded
	if err != nil {
		run.Status, run.Error = "failed", err.Error()
	}
	if recErr := l.Store.RecordRun(ctx, run); recErr != nil {
		return run, errors.Join(err, fmt.Errorf("record etl run: %w", recErr))
	}
	return run, err
}
