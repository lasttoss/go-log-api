# Measurements

The README quotes a per-package table and a total, and the total needs a real PostgreSQL: without one
the store's tests skip, and the number printed is not the service.

## Environment

| | |
|---|---|
| Machine | 12-core x86-64 (AMD64), Ubuntu 24.04, Docker 29.1.3 |
| Toolchain | Go 1.27.2 / Node 24.19.0 / JDK 17 in `maven:3.9-eclipse-temurin-17` |
| Date | 2026-10-13 (UTC) |


## 76.5% of statements, with a database

```
$ docker run -d --name logapi-pg -e POSTGRES_PASSWORD=pg -e POSTGRES_DB=logapi \
    -p 127.0.0.1:15436:5432 postgres:15
$ TEST_DATABASE_URL='postgres://postgres:pg@127.0.0.1:15436/logapi?sslmode=disable' \
    go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
total:  (statements)  76.5%
```

Per package, the same run:

```
cmd/server        0.0%     (flags and wiring; the smoke test in `make up` is what covers it)
internal/config   94.1%
internal/ingest   86.9%
internal/metrics  100.0%
internal/store    88.3%
```

**Result.** Exact, including every row of the README's table. Without `TEST_DATABASE_URL` the same
command prints **61.9%** for the module and **18.3%** for `internal/store`, because the store's tests
skip and say so rather than passing quietly - which is the difference the README points at.

## The backpressure claim

`503 under backpressure instead of unbounded memory` is a behaviour, not a number, and it is asserted
by a test: the ingest handler refuses once its queue is full and reports the refusal, instead of
growing. `make test` covers it with no database and no network.
