// Command etl loads bot state and log exports into the metrics database.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/etl"
	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

func main() {
	dbPath := flag.String("db", envOr("BFWG_DB_PATH", "bfwg-metrics.db"), "SQLite database path")
	gamesPath := flag.String("games", os.Getenv("BFWG_GAMES_PATH"), "club_night_games.json to load")
	logsPath := flag.String("command-logs", os.Getenv("BFWG_COMMAND_LOGS_PATH"),
		"Cloud Logging JSON export (gcloud logging read --format=json) to load")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if *gamesPath == "" && *logsPath == "" {
		logger.Error("nothing to load: pass -games and/or -command-logs")
		os.Exit(2)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	loader := etl.Loader{Store: st, Now: time.Now}
	var failed error
	if *gamesPath != "" {
		run, err := loader.LoadGames(ctx, *gamesPath)
		failed = errors.Join(failed, report(logger, run, err))
	}
	if *logsPath != "" {
		run, err := loader.LoadCommandLogs(ctx, *logsPath)
		failed = errors.Join(failed, report(logger, run, err))
	}
	if failed != nil {
		st.Close()
		os.Exit(1)
	}
}

func report(logger *slog.Logger, run store.Run, err error) error {
	attrs := []any{"source", run.Source, "status", run.Status,
		"rows_read", run.RowsRead, "rows_loaded", run.RowsLoaded,
		"elapsed_ms", run.FinishedAt.Sub(run.StartedAt).Milliseconds()}
	if err != nil {
		logger.Error("etl run failed", append(attrs, "err", err)...)
		return err
	}
	logger.Info("etl run complete", attrs...)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
