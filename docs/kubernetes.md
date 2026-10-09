# Running this service on Kubernetes

The chart in [`charts/gamelog-api`](../charts/gamelog-api) is the short version of this document.
What follows is why it looks the way it does, and what to change when the service grows.

```bash
# For a cluster that can pull from your own registry:
helm install gamelog charts/gamelog-api \
  --set database.url='postgres://user:password@postgres:5432/gamelogs?sslmode=disable'

# Or for a local cluster, with the image built here rather than pulled:
docker build -t ghcr.io/lasttoss/go-log-api:1.0.0 .
kind load docker-image ghcr.io/lasttoss/go-log-api:1.0.0
helm install gamelog charts/gamelog-api \
  --set image.pullPolicy=Never \
  --set database.url='postgres://postgres:localdb@dev-postgres:5432/gamelogs?sslmode=disable'
```

`helm test` asks the service, from a pod inside the cluster, whether it is healthy - which also
proves the pod can still reach Postgres.

## What the application does at boot that shapes the deployment

Two lines of `cmd/server/main.go` decide most of this chart:

```go
s, err := store.Open(ctx, cfg.DatabaseURL)   // exits if Postgres is unreachable
if err := s.Migrate(ctx); err != nil { ... } // creates its own schema
```

**It migrates when it starts.** There is no migration Job to order before the Deployment, because
the binary does it itself; the Deployment is therefore also the migration. Two replicas starting
together would migrate together, which is why `replicaCount` is 1 (see *Scaling out* below). A pod
that starts before Postgres exists exits and is restarted with exponential backoff, which is a
perfectly good wait loop; `waitForDatabase.enabled=true` adds an init container that polls
`pg_isready` instead, so the first boot is quiet rather than crash-looping.

**`DATABASE_URL` has a default in the code**: `postgres://postgres:localdb@localhost:5432/gamelogs`.
For a container that default points at itself and turns a forgotten value into a crash loop with a
confusing error, so the chart refuses to render without `database.url` or `database.existingSecret`
rather than fall through to it. The connection string is kept in a Secret, never in the values in
the clear when it can be avoided.

## Probes: one of them touches the database, the other must not

`/healthz` pings Postgres and answers 503 when the database is gone. That makes it a good
**readiness** probe - a pod that cannot reach its database stops receiving traffic - and a bad
**liveness** probe, because then a database blip restarts every pod that is still perfectly healthy
and each restart runs another migration. The chart therefore uses:

| Probe | Check | Why |
|---|---|---|
| startup | `GET /healthz` | the first check happens after the schema migration, which on a cold database can take a moment |
| readiness | `GET /healthz` | traffic only goes to a pod that can reach its database |
| liveness | `tcpSocket` | it only asks whether this process is still serving, which is the question a restart can fix |

## Shutting down without losing events

The ingest path holds events in memory (`BATCH_SIZE`, `FLUSH_INTERVAL`) and writes them in batches.
On `SIGTERM` the service flushes what it is holding:

```go
_ = server.Shutdown(shutdownCtx)   // 10 second budget
batcher.Flush(shutdownCtx)
```

So `terminationGracePeriodSeconds` is 30, comfortably longer than that 10 second budget, and the
rollout strategy is `maxUnavailable: 0` - a replacement comes up before the old pod is asked to
leave. With the default grace period a slow write would be cut off by `SIGKILL` and the events in
the batch would be lost with nothing in the logs to say so.

## Scaling out

The rollup worker (`ROLLUP_INTERVAL`) starts in every replica and rewrites today's aggregates. It
is idempotent - it writes upserts per (game, day) - so two replicas doing it is wasteful rather than
wrong, but not free:

- **Migrations.** With more than one replica, several pods may run `Migrate` at the same instant on
  a rollout. Move it out of the binary into a Job (`helm.sh/hook: pre-install,pre-upgrade`), or take
  a Postgres advisory lock around it, before raising `replicaCount`.
- **The rollup.** Keep exactly one replica running the rollup - either by leaving `replicaCount` at
  1 and scaling the *ingest* path only, or by adding a leader election / `SELECT ... FOR UPDATE
  SKIP LOCKED` style claim so only one worker does the work.

`autoscaling.enabled=true` renders a HorizontalPodAutoscaler, and it is off by default for exactly
the reason above: horizontal scaling wants the ingest to be stateless first. A PodDisruptionBudget
(`podDisruptionBudget.enabled=true`) is the safe half of the same idea and is what keeps a node
drain from taking the only replica away.

## Metrics

`/metrics` is a Prometheus endpoint in the standard format. `metrics.serviceMonitor.enabled=true`
adds a ServiceMonitor for the Prometheus Operator; the same values work with a plain scrape config
by annotating the Pods through `podAnnotations`.

## Reading the numbers in the logs

```bash
kubectl logs -l app.kubernetes.io/instance=gamelog -f
kubectl port-forward svc/gamelog-gamelog-api 8080:8080
curl -s localhost:8080/v1/games/stats | head -c 500
```

The service logs a rollup line every `ROLLUP_INTERVAL`; if the ingest is the part you are watching,
`/metrics` is the cheap way to see it, because it does not need the database to answer.
