# go-log-api

**Event ingest and metrics rollup API for games, in Go.** Game servers post batches of gameplay
events; the service validates them, writes them through a batcher into PostgreSQL, recomputes
daily metrics (events, DAU, revenue) with SQL, and serves those rollups back to dashboards.

```
$ make up
  [PASS] api is healthy - http://127.0.0.1:8080
  [PASS] ingest accepted a batch - {"accepted": 5, "problems": [], "rejected": 0}
  [PASS] a bad event is rejected with a reason - [{"error": "game_id is required", "index": "0"}]
  [PASS] an oversized batch is refused - HTTP 413
  [PASS] rollup produced a daily row - {"game_id": "cozy-garden-898a9f", "day": "2026-10-09", "events": 5, "dau": 2, "value_sum": 4.99}
  [PASS] the rollup counted every event - events=5
  [PASS] the rollup counted distinct players - dau=2
  [PASS] the rollup summed the value column - value_sum=4.99
  [PASS] prometheus metrics are exposed
OK - ingest, validation, batching, rollup and the read path all work
```

## What this demonstrates

- **Validation at the edge.** Every event is checked in `internal/ingest/event.go` (required
  fields, length limits, RFC3339 timestamps, a clock-skew window) before it reaches the buffer, so
  the write path and the rollups never see half-filled rows. Rejections come back with the index
  and the reason instead of a silent `200`.
- **Batching, because one INSERT per event does not scale.** `Batcher` accumulates events and
  flushes on size or on a timer, writing each batch as a single multi-row `INSERT`. It also flushes
  on shutdown, so a deploy does not lose the last second of events.
- **Backpressure instead of an unbounded queue.** When the buffer is full the API answers `503`
  and tells the caller to retry, which keeps a traffic spike from turning into an OOM kill.
- **Rollups computed in SQL.** `store.Rollup` aggregates in one
  `INSERT ... SELECT ... GROUP BY ... ON CONFLICT DO UPDATE`: counting distinct players per game
  per day is a database problem, and streaming every event into the application to count them
  would be the slowest possible way to answer it.
- **Reads that are cheap by construction.** The API serves `metrics_daily`, not `events`, so a
  dashboard query costs the same whether one player or ten thousand played that day.
- **Metrics that mirror the pipeline.** `/metrics` exposes accepted, rejected, rows written, flush
  errors, rollup runs and the total time spent writing batches - the counters that show which stage
  is the bottleneck.
- **Tests that test behaviour.** Table-driven validation cases, batcher tests for the full-batch /
  flush / buffer-full paths, and config tests for defaults, environment overrides and the
  invariant `BATCH_SIZE <= MAX_BATCH`. All of it runs under `-race`.

## API

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/events` | `{"events":[{game_id, user_id, type, value?, occurred_at?}]}` -> `202` with per-batch counts |
| GET | `/v1/games/stats?game_id=&from=&to=` | daily rollups: `events`, `dau`, `value_sum` |
| GET | `/healthz` | liveness (pings PostgreSQL) |
| GET | `/metrics` | Prometheus text exposition |

Errors: `400` invalid body or nothing accepted, `413` batch larger than `MAX_BATCH`, `503` buffer
full (retry shortly).

## Quickstart

Requires Docker with the compose plugin.

```bash
git clone https://github.com/lasttoss/go-log-api.git
cd go-log-api
make up          # postgres + api, then the end-to-end smoke test
make logs
make down
```

Ports taken? Copy `.env.example` to `.env` and change `API_PORT` / `POSTGRES_PORT`.

By hand:

```bash
curl -s localhost:8080/v1/events -H 'Content-Type: application/json' -d '{"events":[
  {"game_id":"cozy-garden","user_id":"u-1","type":"login"},
  {"game_id":"cozy-garden","user_id":"u-2","type":"purchase","value":4.99}]}'

# the rollup runs every ROLLUP_INTERVAL (10s in compose)
curl -s 'localhost:8080/v1/games/stats?game_id=cozy-garden'
curl -s localhost:8080/metrics | grep rows_written
```

Local development: `make infra` (postgres only), `make run`, `make race`, `make coverage`.

## Running it on Kubernetes

The chart in [`charts/gamelog-api`](charts/gamelog-api) deploys the service with its probes, its
Secret and its PodDisruptionBudget; `helm test` asks it, from inside the cluster, whether it is
healthy. [`docs/kubernetes.md`](docs/kubernetes.md) explains the two things about this application
that shape the deployment: it migrates its own schema at boot, and it flushes the events still in
memory on `SIGTERM`.

```bash
helm install gamelog charts/gamelog-api \
  --set database.url='postgres://user:password@postgres:5432/gamelogs?sslmode=disable'
```

## Configuration

| environment | default | meaning |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `DATABASE_URL` | `postgres://postgres:localdb@localhost:5432/gamelogs?sslmode=disable` | PostgreSQL |
| `BATCH_SIZE` | `500` | rows per write batch |
| `MAX_BATCH` | `1000` | largest accepted request; must be `>= BATCH_SIZE` |
| `FLUSH_INTERVAL` | `2s` | maximum time an event may sit in the buffer |
| `ROLLUP_INTERVAL` | `30s` | how often the daily aggregates are recomputed |

## Schema

```sql
events(id, game_id, user_id, event_type, value, occurred_at, received_at)
metrics_daily(game_id, day, events, dau, value_sum, updated_at)   -- primary key (game_id, day)
```

The schema is created at boot with idempotent `CREATE TABLE IF NOT EXISTS` statements: a service
that needs a manual migration step before it does anything is a service nobody runs twice. A real
deployment would move this to versioned migrations.

## Design notes

- **Why PostgreSQL and not a column store?** This is the ingest-and-rollup layer for a handful of
  games: one database handles both the write and the read, and the rollup table keeps the read side
  flat. The companion repository [architecture-diagrams](https://github.com/lasttoss/architecture-diagrams)
  shows where a column store (ClickHouse) and an orchestrator (Airflow) do earn their place once
  the volume justifies them.
- **Why is the rollup a ticker instead of a trigger?** A trigger runs inside the write transaction
  and would put aggregation on the ingest latency path. Keeping the two apart means a slow rollup
  cannot make ingest slow.
- **Known gap:** the rollup recomputes whole days over a 7 day window rather than incrementally.
  At higher volume the window would become hourly buckets maintained by a background worker.

## License

MIT - see [LICENSE](LICENSE). Dependencies keep their own licenses (`go.mod`).

## The pipeline as a picture

```mermaid
%% Source for docs/diagrams/ingest-pipeline.html
%% Where a batch of events goes, and where it is refused.
flowchart LR
  G["game servers<br/>batches of events"] -->|"POST /v1/events"| V{"validate<br/>400 · 413 oversized"}
  V -->|"accepted"| BUF["in-memory buffer<br/>full → 503"]
  BUF --> B["batcher<br/>flush on 500 rows<br/>or on a timer"]
  B -->|"one multi-row INSERT"| PG[("PostgreSQL")]
  PG --> R["rollup<br/>recompute"]
  R --> Q["read path<br/>GET stats"]
  B -.-> M["counters per stage:<br/>accepted · rejected · flush errors<br/>rollup runs · write time"]
  classDef gate fill:#eef5ef,stroke:#1a6b3c,stroke-width:2px;
  class V,BUF,B gate;
```

The request path, and the two places it is refused: a `413` is a caller sending more than `MAX_BATCH` — the
size of a write is a decision the server makes — and a `503` is the buffer being full, which is refusing
load while there is still capacity to answer rather than queueing until there is none.

The counters are drawn on the picture because they are what locates a slow run: accepted and rejected per
stage, flush errors, rollup runs, and the total time spent writing batches.

`docs/diagrams/ingest-pipeline.mmd` is the Mermaid source; `make diagram` exports a PNG if a browser is
present.

## Coverage

`make test` runs the unit tests; the store tests need a real PostgreSQL and skip themselves when
`TEST_DATABASE_URL` is not set. That is how CI runs them too, against a postgres service container on the build
job, so the number printed there is the whole service:

| package | statements |
|---|---|
| `internal/metrics` | 100.0% |
| `internal/config` | 94.1% |
| `internal/store` | 88.3% |
| `internal/ingest` | 86.9% |
| `cmd/server` | 0.0% |
| total | **76.5%** |

`cmd/server` is flags and wiring, and what covers it is the smoke test in `make up`. Everything else is measured
by the tests, and reproduced like this:

```bash
make infra
TEST_DATABASE_URL='postgres://postgres:localdb@localhost:5432/gamelogs?sslmode=disable' make coverage
```

Two things worth knowing before running that. The store tests **own the database they are pointed at** - they drop
`events` and `metrics_daily` first so the migrations have something to create - so point them at a throwaway
PostgreSQL. And they skip rather than fail without a database, which keeps `make test` usable on a laptop that has
no postgres, at the cost of a coverage number that is quietly too low if you forget.
