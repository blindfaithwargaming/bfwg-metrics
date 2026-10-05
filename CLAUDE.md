# bfwg-metrics — Claude Code Context

Public Go service publishing aggregate metrics for the BFWG Discord bot (sibling
repo at `../discord-bot`). Portfolio project: the owner is using it to learn and demonstrate Go, ETL,
SQL, APIs, Docker, Kubernetes and monitoring. Explain idioms in commit messages
and PRs when they are non-obvious; the owner is newer to Go than to Python.

## Commands

```
make test     # go test -race ./...
make lint     # gofmt + go vet
make run      # load testdata, serve :8080
```

## Rules

- **Public repo, private members.** Never commit real bot exports, and never decode
  member identifiers, display names, thread titles or notes. Any new source or
  endpoint needs a case in the privacy tests (ADR 0002). Real data goes in `private/`.
- **All SQL lives in `internal/store`.** Handlers and the ETL call store methods.
- **Tests run against real SQLite in `t.TempDir()`.** No mocking the store.
  Inject clocks (`Now`) rather than relying on wall time.
- **Prefer the standard library.** Current deps: Prometheus client and modernc SQLite.
  Justify any new dependency.
- **Keep `api/openapi.yaml` in step** with handler behaviour.
- **Branching:** `feature/<slug>` → PR into `main`; the owner reviews diffs.
- No comments describing what code does; only non-obvious *why*.
