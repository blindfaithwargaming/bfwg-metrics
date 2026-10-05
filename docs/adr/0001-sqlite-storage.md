# 0001 – SQLite for storage

**Status:** accepted (2026-10-05)

## Context

The dataset is small: hundreds of games and tens of thousands of command
invocations a year. There is one reader process (the server) and one periodic writer (the ETL job).
The bot itself runs on a free-tier VM, and this service should be just as cheap to run.

## Decision

Use SQLite through `modernc.org/sqlite`, a pure-Go driver with no cgo. This
keeps the image static and distroless. WAL mode lets the server read while the ETL job writes, and
a 5 s busy timeout covers the brief writer lock.

## Consequences

- No database server to run, back up or pay for. The database file is the backup.
- The server and the ETL job must share a filesystem, which on Kubernetes means one
  RWO volume and both pods on the same node. This is the main limit on scaling.
- The SQL keeps to standard features (CTEs, window functions) so that
  moving to Postgres is mechanical when needed (see roadmap).
