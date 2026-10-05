CREATE TABLE command_invocations (
    source_key  TEXT PRIMARY KEY,
    occurred_at TEXT    NOT NULL,
    command     TEXT    NOT NULL,
    success     INTEGER NOT NULL CHECK (success IN (0, 1)),
    elapsed_ms  INTEGER NOT NULL CHECK (elapsed_ms >= 0)
);

CREATE INDEX idx_command_invocations_occurred_at ON command_invocations (occurred_at);

CREATE TABLE games (
    id              TEXT PRIMARY KEY,
    club_night_date TEXT NOT NULL,
    reported_at     TEXT NOT NULL,
    game            TEXT NOT NULL,
    result          TEXT NOT NULL CHECK (result IN ('side1', 'side2', 'draw')),
    points_size     INTEGER
);

CREATE TABLE game_factions (
    game_id TEXT NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    side    TEXT NOT NULL CHECK (side IN ('side1', 'side2')),
    faction TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('win', 'loss', 'draw'))
);

CREATE INDEX idx_game_factions_faction ON game_factions (faction);

CREATE TABLE etl_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT    NOT NULL,
    started_at  TEXT    NOT NULL,
    finished_at TEXT    NOT NULL,
    status      TEXT    NOT NULL CHECK (status IN ('ok', 'failed')),
    rows_read   INTEGER NOT NULL DEFAULT 0,
    rows_loaded INTEGER NOT NULL DEFAULT 0,
    error       TEXT
);
