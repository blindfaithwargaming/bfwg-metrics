# Roadmap

Ordered by what each step adds, not by size.

1. **Extract from source directly.** Add a GCS extractor, using Workload Identity with no keys, and a
   Cloud Logging extractor, so the CronJob reads the bot's private bucket and log stream
   itself instead of reading pre-exported files.
2. **API gateway.** Put the API behind Gravitee APIM (the community edition, run
   in Compose). Import `api/openapi.yaml`, apply a rate-limit plan and an API key
   plan, and compare gateway analytics with this service's own metrics.
3. **Postgres.** Replace SQLite so the server and ETL can scale independently
   (ADR 0001). The SQL is standard apart from `INSERT OR IGNORE`, which becomes
   `ON CONFLICT DO NOTHING`.
4. **Grafana dashboard** provisioned from JSON, alongside the existing alert rules.
5. **Charts on the HTML dashboard**: command volume over time, and faction win
   rates with sample-size context.
6. **Helm chart** generated from the kustomize base, for parameterised installs.
7. **ETL tooling comparison.** Re-implement the games pipeline in a visual ETL
   tool such as Pentaho Data Integration, and write up the trade-offs against hand-written Go.
