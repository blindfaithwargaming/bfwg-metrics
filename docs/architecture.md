# Architecture

## Components

**`cmd/etl`** is a run-to-completion job, scheduled as a Kubernetes CronJob. Each
source is processed independently, and every run, whether it succeeds or fails, writes one row
to `etl_runs`.

| Source | Format | Load strategy | Why |
|---|---|---|---|
| `games` | Bot's `club_night_games.json` | Full snapshot replace in one transaction | Members can delete games with `/club-night remove-game`, so an append-only load would resurrect them. The file is small. |
| `command_logs` | `gcloud logging read --format=json` export | Append with `INSERT OR IGNORE` keyed on `insertId` | Log exports overlap from run to run. Deduplication makes reloads safe and keeps history past the log retention window. |

**`cmd/server`** is a read-only HTTP service. It never writes to the database,
so a failed ETL run degrades freshness but not availability.

**`internal/store`** owns all SQL. Migrations are embedded and applied
transactionally on open; `schema_migrations` records which have run.

## Data model

```
command_invocations  source_key PK · occurred_at · command · success · elapsed_ms
games                id PK · club_night_date · reported_at · game · result · points_size
game_factions        game_id FK→games (cascade) · side · faction · outcome
etl_runs             id · source · started_at · finished_at · status · rows_read · rows_loaded · error
```

Timestamps are stored as fixed-width UTC strings (`2006-01-02T15:04:05.000Z`)
so range filters and ordering work lexicographically.

## Request path

```
client → instrument (RED metrics, access log) → ServeMux → handler → store → SQLite
```

Metrics are labelled by mux pattern (`GET /api/v1/games/summary`), not raw
URL, to keep Prometheus label cardinality bounded. ETL health is exposed by a
custom collector that reads `etl_runs` at scrape time, because the ETL job is a
separate short-lived process with nothing to scrape.

## Relationship to the bot

The bot (separate repository) is unaware of this service. Integration is
one-way and read-only. This service consumes artefacts the bot already emits for
other reasons: its private state mirror and its structured logs. Changing a bot log format
would break the `command_logs` parser. The regex is covered by tests using the
exact line format from the bot's `command_context`.
