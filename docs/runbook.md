# Runbook

## Service levels

| Indicator | Target | Measured by |
|---|---|---|
| API availability | 99% of requests non-5xx over 30 days | `bfwg_http_requests_total` |
| API latency | p95 under 250 ms | `bfwg_http_request_duration_seconds` |
| Data freshness | Each source loaded within the last 48 h | `bfwg_etl_last_run_timestamp_seconds` |

These targets suit a volunteer-run club tool. The point is to have them written down and measured.

## Triage

1. Check `/readyz`. A 503 means the server cannot reach its database.
2. Check `/api/v1/etl/runs` for the latest status and error per source.
3. Read the structured logs: `kubectl -n bfwg-metrics logs deploy/bfwg-metrics` or
   `kubectl -n bfwg-metrics logs job/<latest-etl-job>`.
4. Record the incident, its cause and its fix as an issue. If the cause is
   likely to recur, add it to **Known errors** below.

## Alerts

### ETL run failed

`bfwg_etl_last_run_success == 0`. The `error` field on `/api/v1/etl/runs` names the cause.

- *File not found / decode error*: the export is missing or truncated. Re-export it and re-run:
  `kubectl -n bfwg-metrics create job etl-manual-$(date +%s) --from=cronjob/bfwg-metrics-etl`.
- *Database locked*: another writer held the database past the 5 s busy timeout.
  Check for overlapping jobs (`concurrencyPolicy: Forbid` should prevent them).

Impact: dashboard data is stale but still served. The failure does not reach users.

### ETL data stale

No run for a source in 48 h. Check that the CronJob is not suspended and that recent jobs were scheduled:
`kubectl -n bfwg-metrics get cronjob,jobs`.

### API high error rate

More than 5% of requests returned 5xx for 10 minutes. Check readiness and logs for
`err` fields, and check whether the PVC is full: `kubectl -n bfwg-metrics describe pvc`.

## Known errors

| Symptom | Cause | Workaround |
|---|---|---|
| `command_logs` loads 0 rows but reads many | Export contains only `invoked` lines, or the bot's log format changed | Confirm the export filter includes `done`. If the format changed, update the regex in `internal/etl/commandlogs.go` and its tests. |
| ETL pod stuck `Pending` on a multi-node cluster | RWO volume is attached to the server's node | Pin both workloads to one node, or move to Postgres (ADR 0001). |
