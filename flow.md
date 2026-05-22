# Ledger Platform — Technical Flow Reference

Everything installed, created, or configured in this project — versions, files, ports, credentials — phase by phase.

---

## Phase 1 — Repo Scaffold + Shared Libraries

### Runtime

| Tool | Version |
|------|---------|
| Go | 1.26.1 (minimum required: 1.22) |
| Node.js | 18.19.1 (required for UI build) |
| npm | 9.2.0 |

### Go Workspace

| File | Purpose |
|------|---------|
| `go.work` | Workspace file — links all modules locally without publishing |
| `go.work.sum` | Workspace checksum file (auto-generated) |

### Modules created

| Module path | Location | Purpose |
|-------------|----------|---------|
| `github.com/prasham/ledger/internal` | `internal/` | Shared libraries used by all services |
| `github.com/prasham/ledger/services/ledger-svc` | `services/ledger-svc/` | Built in Phase 5 |
| `github.com/prasham/ledger/services/account-svc` | `services/account-svc/` | Built in Phase 6 |
| `github.com/prasham/ledger/services/ui-bff` | `services/ui-bff/` | Built in UI phase — REST → gRPC bridge + serves React app |
| `github.com/prasham/ledger/services/fx-svc` | `services/fx-svc/` | Stub — built in Phase 7 |
| `github.com/prasham/ledger/services/transaction-svc` | `services/transaction-svc/` | Stub — built in Phase 8 |
| `github.com/prasham/ledger/services/payout-svc` | `services/payout-svc/` | Stub — built in Phase 9 |
| `github.com/prasham/ledger/services/recon-worker` | `services/recon-worker/` | Stub — built in Phase 9 |
| `github.com/prasham/ledger/services/gateway` | `services/gateway/` | Stub — built in Phase 10 |
| `github.com/prasham/ledger/services/consumers` | `services/consumers/` | Stub — built in Phase 11 |

### Go packages installed (internal module)

| Package | Version | Why |
|---------|---------|-----|
| `github.com/shopspring/decimal` | v1.4.0 | Exact decimal arithmetic for money — no float64 |
| `github.com/rs/zerolog` | v1.33.0 | Structured JSON logging |
| `github.com/google/uuid` | v1.6.0 | UUID v4 generation for all IDs |
| `github.com/redis/go-redis/v9` | v9.7.0 | Redis client for idempotency store |
| `github.com/stretchr/testify` | v1.9.0 | Test assertions |
| `go.opentelemetry.io/otel` | v1.31.0 | OpenTelemetry tracing API |
| `go.opentelemetry.io/otel/sdk` | v1.31.0 | OTel SDK (TracerProvider, SpanProcessor) |
| `go.opentelemetry.io/otel/trace` | v1.31.0 | Tracer interface |

### Packages created

| Package | File(s) | What it does |
|---------|---------|--------------|
| `internal/money` | `money.go`, `currency.go` | `Amount` type wrapping decimal, `Currency` ISO 4217 codes |
| `internal/errors` | `errors.go` | Typed errors: `NotFoundError`, `ValidationError`, `ConflictError`, `InternalError` |
| `internal/logging` | `logging.go` | zerolog bootstrap with `service` + `timestamp` fields |
| `internal/tracing` | `tracing.go` | OTel `Init(ctx, Config, SpanProcessor)` + `Tracer(name)` |
| `internal/idempotency` | `idempotency.go` | Redis `Store` interface + `RedisStore` with `SetNX` (24h TTL) |
| `internal/outbox` | `outbox.go` | `Message` struct, `EventType` constants, `Inserter` interface |

### Config files

| File | Purpose |
|------|---------|
| `.gitignore` | Excludes binaries, `.env`, generated code, local volumes |
| `.golangci.yml` | Linting rules: `wrapcheck`, `gosec`, `errcheck`, `goimports` |
| `Makefile` | `make test`, `make test-race`, `make lint`, `make build`, `make tidy`, `make infra-up/down/clean` |

### Test results (Phase 1)

```
ok  github.com/prasham/ledger/internal/errors       (race detector: pass)
ok  github.com/prasham/ledger/internal/idempotency  (race detector: pass)
ok  github.com/prasham/ledger/internal/logging      (race detector: pass)
ok  github.com/prasham/ledger/internal/money        (race detector: pass)
ok  github.com/prasham/ledger/internal/outbox       (race detector: pass)
ok  github.com/prasham/ledger/internal/tracing      (race detector: pass)
```

---

## Phase 2 — Docker Compose Infrastructure

### Docker versions

| Tool | Version |
|------|---------|
| Docker Engine | 29.4.2 |
| Docker Compose | v5.1.3 |

### Docker images and versions

| Container name | Image | Version | Purpose |
|----------------|-------|---------|---------|
| `ledger-postgres` | `postgres` | `16-alpine` | Source of truth, wal_level=logical |
| `ledger-redis` | `redis` | `7-alpine` | Idempotency, rate limits, Redlock |
| `ledger-redpanda` | `redpandadata/redpanda` | `v24.2.7` | Kafka-compatible broker |
| `ledger-redpanda-init` | `redpandadata/redpanda` | `v24.2.7` | One-shot topic creation |
| `ledger-redpanda-console` | `redpandadata/console` | `v2.8.11` | Kafka browser UI |
| `ledger-apicurio` | `apicurio/apicurio-registry-mem` | `2.6.3.Final` | Avro schema registry |
| `ledger-minio` | `minio/minio` | `RELEASE.2024-11-07T00-52-20Z` | S3-compatible file storage |
| `ledger-minio-init` | `minio/mc` | `RELEASE.2024-11-17T19-35-25Z` | One-shot bucket creation |
| `ledger-debezium` | `debezium/connect` | `2.7.3.Final` | Postgres WAL → Kafka CDC |
| `ledger-debezium-init` | `curlimages/curl` | `8.10.1` | One-shot connector registration |
| `ledger-pgadmin` | `dpage/pgadmin4` | `9.15.0` | PostgreSQL browser UI |
| `ledger-redisinsight` | `redis/redisinsight` | `2.44.0` | Redis browser UI |
| `ledger-toxiproxy` | `ghcr.io/shopify/toxiproxy` | `2.9.0` | Chaos testing proxy |

### Port mapping

| Port (host) | Container | Service | Purpose |
|-------------|-----------|---------|---------|
| `5432` | `ledger-postgres` | PostgreSQL | Database connections |
| `6379` | `ledger-redis` | Redis | Cache + lock connections |
| `9092` | `ledger-redpanda` | Redpanda | Kafka API (Docker internal) |
| `19092` | `ledger-redpanda` | Redpanda | Kafka API (host / integration tests) |
| `9644` | `ledger-redpanda` | Redpanda | Admin API |
| `8082` | `ledger-apicurio` | Apicurio | Schema registry + UI |
| `9000` | `ledger-minio` | MinIO | S3 API |
| `9001` | `ledger-minio` | MinIO | Console UI |
| `8083` | `ledger-debezium` | Debezium | Connect REST API |
| `8085` | `ledger-redpanda-console` | Console | Kafka browser UI |
| `5050` | `ledger-pgadmin` | pgAdmin 4 | PostgreSQL browser UI |
| `5540` | `ledger-redisinsight` | RedisInsight | Redis browser UI |
| `8474` | `ledger-toxiproxy` | Toxiproxy | Chaos API |
| `15432` | `ledger-toxiproxy` | Toxiproxy | Proxy → Postgres |
| `16379` | `ledger-toxiproxy` | Toxiproxy | Proxy → Redis |
| `19093` | `ledger-toxiproxy` | Toxiproxy | Proxy → Redpanda |

### Go service ports

| Port | Binary | Protocol | Purpose |
|------|--------|----------|---------|
| `50051` | `ledger-svc` | gRPC | LedgerService RPCs |
| `9101` | `ledger-svc` | HTTP | `/metrics` + `/healthz` |
| `50052` | `account-svc` | gRPC | AccountService RPCs |
| `9102` | `account-svc` | HTTP | `/metrics` + `/healthz` |
| `50053` | `fx-svc` | gRPC | FXService RPCs |
| `9103` | `fx-svc` | HTTP | `/metrics` + `/healthz` |
| `50054` | `transaction-svc` | gRPC | TransactionService RPCs |
| `9104` | `transaction-svc` | HTTP | `/metrics` + `/healthz` |
| `50055` | `payout-svc` | gRPC | PayoutService RPCs |
| `9105` | `payout-svc` | HTTP | `/metrics` + `/healthz` |
| `9106` | `recon-worker` | HTTP | `/metrics` + `/healthz` (no gRPC) |
| `8090` | `ui-bff` | HTTP | REST API + React app (production) |
| `5173` | Vite dev server | HTTP | React app with hot-reload (dev only) |

### Credentials (local dev only)

> Full reference: see top section of `procedure.md`
> Values stored in `.env` (git-ignored). Never hardcoded in any committed file.

| Service | Variable | Value |
|---------|----------|-------|
| PostgreSQL | `POSTGRES_USER` | `ledger` |
| PostgreSQL | `POSTGRES_PASSWORD` | `ledger` |
| PostgreSQL | `POSTGRES_DB` | `ledger` |
| MinIO | `MINIO_ROOT_USER` | `minioadmin` |
| MinIO | `MINIO_ROOT_PASSWORD` | `minioadmin` |
| Redis | — | No password |
| Redpanda | — | No auth |

### PostgreSQL schemas created

| Schema | Owned by | Purpose |
|--------|----------|---------|
| `transaction` | transaction-svc | Saga rows, outbox table |
| `ledger` | ledger-svc | Append-only double-entry entries |
| `account` | account-svc | Wallets, holds, reserves |
| `fx` | fx-svc | Rate cache, quote locks |
| `payout` | payout-svc | Withdrawal batches, bank files |
| `consumer` | consumers | Audit log, notification state |

### Postgres configuration flags

| Flag | Value | Why |
|------|-------|-----|
| `wal_level` | `logical` | Required for Debezium logical replication |
| `max_replication_slots` | `10` | Allows multiple Debezium connector slots |
| `max_wal_senders` | `10` | Allows WAL streaming to multiple receivers |
| `shared_preload_libraries` | `pg_stat_statements` | Slow query tracking |
| `log_min_duration_statement` | `200ms` | Log queries slower than 200ms |

### Kafka topics created

| Topic | Partitions | Cleanup policy | Retention | Purpose |
|-------|-----------|----------------|-----------|---------|
| `payments.payment.completed` | 10 | delete | 7 days | Successful payment events |
| `payments.payment.failed` | 10 | delete | 7 days | Failed / rolled-back events |
| `payments.payout.initiated` | 10 | delete | 7 days | Payout batch started |
| `payments.payout.finalized` | 10 | delete | 7 days | Bank confirmed payout |
| `payments.hold.matured` | 10 | delete | 7 days | T+2 hold released |
| `payments.recon.drift` | 3 | delete | 7 days | Financial invariant violation |
| `payments.dlq.audit` | 3 | delete | 30 days | Audit consumer dead-letter |
| `payments.dlq.notification` | 3 | delete | 30 days | Notification consumer dead-letter |
| `payments.dlq.analytics` | 3 | delete | 30 days | Analytics consumer dead-letter |
| `debezium.configs` | 1 | **compact** | — | Kafka Connect config storage |
| `debezium.offsets` | 25 | **compact** | — | Kafka Connect offset storage |
| `debezium.status` | 5 | **compact** | — | Kafka Connect status storage |

> Debezium internal topics **must** use `cleanup.policy=compact`. Using `delete` crashes the Connect worker.

### MinIO buckets created

| Bucket | Access | Purpose |
|--------|--------|---------|
| `settlement-files` | private | NACH / SEPA bank transfer files generated by payout-svc |
| `recon-reports` | private | Reconciliation reports generated by recon-worker |
| `bank-responses` | private | Bank response files uploaded by ops after settlement |

### Debezium connector

| Setting | Value |
|---------|-------|
| Connector name | `ledger-outbox-connector` |
| Connector class | `io.debezium.connector.postgresql.PostgresConnector` |
| Plugin | `pgoutput` (built into Postgres 10+, no extra install) |
| Replication slot | `ledger_outbox_slot` |
| Publication | `ledger_outbox_pub` |
| Tables monitored | `transaction.outbox_messages`, `ledger.outbox_messages`, `account.outbox_messages`, `payout.outbox_messages` |
| SMT | `io.debezium.transforms.outbox.EventRouter` |
| Routing field | `event_type` |
| Topic pattern | `payments.${event_type}` (e.g. `payments.payment.completed`) |
| Publication autocreate mode | `all_tables` (matches the `FOR ALL TABLES` publication created in `02_replication.sql`) |
| Snapshot mode | `never` (outbox is append-only — no historical replay) |

### Toxiproxy proxies

| Proxy name | Listen port | Upstream |
|------------|-------------|----------|
| `postgres` | `15432` | `postgres:5432` |
| `redis` | `16379` | `redis:6379` |
| `redpanda` | `19093` | `redpanda:9092` |

### Infra files created

```
docker-compose.yml
.env                             ← git-ignored, created from .env.example
.env.example                     ← committed template
infra/
├── postgres/init/
│   ├── 00_extensions.sql        pgcrypto, uuid-ossp, pg_stat_statements
│   ├── 01_schemas.sql           6 service schemas + privileges
│   └── 02_replication.sql       replication slot + publication
├── redpanda/
│   ├── create-topics.sh         all Kafka topics
│   └── console-config.yml       Redpanda Console config
├── debezium/
│   ├── outbox-connector.json    connector definition (placeholders, no secrets)
│   └── register-connector.sh   injects credentials from env at runtime
├── minio/
│   └── create-buckets.sh        creates 3 buckets, reads creds from env
└── toxiproxy/
    └── config.json              proxy definitions
```

### Browser UIs

| UI | URL | Login |
|----|-----|-------|
| Redpanda Console | http://localhost:8085 | none |
| MinIO Console | http://localhost:9001 | minioadmin / minioadmin |
| Apicurio Registry | http://localhost:8082/ui | none |
| pgAdmin 4 | http://localhost:5050 | admin@ledger.dev / admin |
| RedisInsight | http://localhost:5540 | none (first launch) |

---

## Phase 3 — Observability Stack

### Docker images added

| Container name | Image | Version | Purpose |
|----------------|-------|---------|---------|
| `ledger-otel-collector` | `otel/opentelemetry-collector-contrib` | `0.104.0` | Receives OTLP traces/metrics from Go services |
| `ledger-prometheus` | `prom/prometheus` | `v2.53.2` | Metrics scrape + storage |
| `ledger-tempo` | `grafana/tempo` | `2.5.0` | Distributed trace storage |
| `ledger-loki` | `grafana/loki` | `3.1.1` | Log aggregation |
| `ledger-promtail` | `grafana/promtail` | `3.1.1` | Ships Docker container logs → Loki |
| `ledger-grafana` | `grafana/grafana` | `11.1.4` | Dashboards (Prometheus + Loki + Tempo) |

### Port mapping (Phase 3 additions)

| Port (host) | Container | Service | Purpose |
|-------------|-----------|---------|---------|
| `4317` | `ledger-otel-collector` | OTel Collector | OTLP gRPC — Go services send traces here |
| `4318` | `ledger-otel-collector` | OTel Collector | OTLP HTTP |
| `8889` | `ledger-otel-collector` | OTel Collector | Prometheus scrape target (re-exports service metrics) |
| `9090` | `ledger-prometheus` | Prometheus | Metrics UI + API |
| `3200` | `ledger-tempo` | Tempo | Trace HTTP API (Grafana datasource) |
| `3100` | `ledger-loki` | Loki | Log HTTP API (Grafana datasource) |
| `3000` | `ledger-grafana` | Grafana | Dashboard UI |

### Credentials

| Service | Variable | Value |
|---------|----------|-------|
| Grafana | `GRAFANA_ADMIN_USER` | `admin` |
| Grafana | `GRAFANA_ADMIN_PASSWORD` | `admin` |

### Data flow

```
Go services ──OTLP gRPC──▶ otel-collector:4317
                                │
                    ┌───────────┴───────────┐
                    ▼                       ▼
              tempo:4317              prometheus:8889
           (trace storage)        (metrics re-export)
                    │                       │
                    └──────────┬────────────┘
                               ▼
                           grafana:3000

Docker container logs ──▶ promtail ──▶ loki:3100 ──▶ grafana:3000
```

### Config files created

```
infra/
├── otel-collector/
│   └── config.yaml          receivers: OTLP gRPC+HTTP; exporters: Tempo + Prometheus
├── prometheus/
│   └── prometheus.yml       scrapes otel-collector:8889 (+ services added in Phase 5+)
├── tempo/
│   └── tempo.yaml           local filesystem backend, 1h retention
├── loki/
│   └── loki.yaml            local filesystem backend, schema v13 (tsdb)
├── promtail/
│   └── promtail.yaml        Docker socket SD, parses zerolog JSON level field
└── grafana/
    ├── provisioning/
    │   ├── datasources/
    │   │   └── datasources.yaml   Prometheus + Loki + Tempo auto-provisioned
    │   └── dashboards/
    │       └── dashboards.yaml    loads from /var/lib/grafana/dashboards
    └── dashboards/                (dashboard JSON files added per service in Phase 5+)
```

### Go package added

| Package | File(s) | What it does |
|---------|---------|--------------|
| `internal/metrics` | `metrics.go` | `Registry` wrapping `prometheus.Registry` with Go/process collectors pre-loaded; `Handler()` for `/metrics`; `NewCounterVec`, `NewHistogramVec`, `NewGaugeVec` helpers |

### Go dependency added

| Package | Version | Why |
|---------|---------|-----|
| `github.com/prometheus/client_golang` | `v1.20.5` | Prometheus metrics instrumentation |

### Browser UIs (Phase 3 additions)

| UI | URL | Login |
|----|-----|-------|
| Grafana | http://localhost:3000 | admin / admin |
| Prometheus | http://localhost:9090 | none |

### Notes

- OTel Collector image is distroless — no shell/wget. Docker healthcheck omitted; the collector's internal `health_check` extension runs on port 13133.
- Promtail mounts `/var/run/docker.sock` (read-only) to discover container logs via Docker service discovery.
- Grafana datasources are auto-provisioned — no manual setup needed after `docker compose up`.
- Prometheus scrape targets for individual Go services are added to `prometheus.yml` in Phase 5+.

### Test results (Phase 3)

```
ok  github.com/prasham/ledger/internal/metrics  (race detector: pass)
```

---

## Phase 4 — Proto Contracts + buf

### Tools installed

| Tool | Version | How installed |
|------|---------|---------------|
| `buf` | `1.69.0` | `go install github.com/bufbuild/buf/cmd/buf@latest` |
| `protoc-gen-go` | `v1.36.11` | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` |
| `protoc-gen-go-grpc` | `1.6.2` | `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` |

### Proto files

| File | Package | Service |
|------|---------|---------|
| `proto/common/v1/money.proto` | `common.v1` | — (shared `Money` message) |
| `proto/ledger/v1/ledger.proto` | `ledger.v1` | `LedgerService` |
| `proto/account/v1/account.proto` | `account.v1` | `AccountService` |
| `proto/fx/v1/fx.proto` | `fx.v1` | `FXService` |
| `proto/transaction/v1/transaction.proto` | `transaction.v1` | `TransactionService` |
| `proto/payout/v1/payout.proto` | `payout.v1` | `PayoutService` |

### Service RPCs

| Service | RPCs |
|---------|------|
| `LedgerService` | `PostEntry`, `GetBalance`, `ListEntries` |
| `AccountService` | `CreateAccount`, `GetAccount`, `ListAccounts`, `PlaceHold`, `ReleaseHold`, `CreditAccount`, `DebitAccount` |
| `FXService` | `GetQuote`, `LockQuote` |
| `TransactionService` | `InitiatePayment`, `GetTransaction` |
| `PayoutService` | `InitiatePayout`, `GetPayout` |

### buf config files

| File | Purpose |
|------|---------|
| `proto/buf.yaml` | Module definition — lint: STANDARD, breaking: FILE |
| `buf.gen.yaml` | Code gen — local `protoc-gen-go` + `protoc-gen-go-grpc`, output → `gen/` |

### Generated files

```
gen/
├── go.mod                          module github.com/prasham/ledger/gen
├── common/v1/money.pb.go
├── account/v1/account.pb.go
├── account/v1/account_grpc.pb.go
├── fx/v1/fx.pb.go
├── fx/v1/fx_grpc.pb.go
├── ledger/v1/ledger.pb.go
├── ledger/v1/ledger_grpc.pb.go
├── payout/v1/payout.pb.go
├── payout/v1/payout_grpc.pb.go
├── transaction/v1/transaction.pb.go
└── transaction/v1/transaction_grpc.pb.go
```

### Go dependencies added (gen module)

| Package | Version |
|---------|---------|
| `google.golang.org/protobuf` | `v1.36.11` |
| `google.golang.org/grpc` | `v1.81.1` |

### go.work updated

`./gen` added to workspace — all services can now import generated types directly.

### Makefile targets added

| Target | Command | Purpose |
|--------|---------|---------|
| `make proto-lint` | `buf lint proto` | Lint all proto files |
| `make proto-gen` | `buf generate` | Regenerate Go code from protos |

---

## Phase 5 — ledger-svc

### What it does
Append-only double-entry ledger. Every PostEntry writes exactly two rows (DEBIT + CREDIT) in a single transaction. Balances are never stored — they are projected by `SUM(credits) - SUM(debits)`.

### Service ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `50051` | gRPC | LedgerService RPCs |
| `9101` | HTTP | `/metrics` (Prometheus) + `/healthz` |

### gRPC RPCs

| RPC | What it does |
|-----|-------------|
| `PostEntry` | Writes debit + credit row in one DB transaction. Idempotency via Redis SetNX. |
| `GetBalance` | Projects balance: `SUM(CASE WHEN type=CREDIT THEN amount ELSE -amount END)` |
| `ListEntries` | Returns most recent entries for an account (newest first, max 100) |

### Database schema (ledger schema)

| Table | Purpose |
|-------|---------|
| `ledger.entries` | Append-only double-entry rows (DEBIT / CREDIT) |
| `ledger.outbox_messages` | CDC outbox (Debezium tails via WAL) |

### Files created

```
services/ledger-svc/
├── cmd/ledger-svc/main.go         gRPC + metrics server, graceful shutdown
├── internal/
│   ├── config/config.go           env-based config (ports, DSN, Redis addr)
│   ├── db/
│   │   ├── migrate.go             idempotent SQL migration runner
│   │   └── repo.go                PostDoubleEntry, GetBalance, ListEntries
│   └── service/
│       └── ledger.go              LedgerServiceServer implementation
├── migrations/001_init.up.sql     entries + outbox_messages DDL
└── go.mod
```

### Key dependencies added

| Package | Version | Why |
|---------|---------|-----|
| `github.com/jackc/pgx/v5` | `v5.9.2` | PostgreSQL driver + connection pool |
| `google.golang.org/grpc` | `v1.81.1` | gRPC server |
| `google.golang.org/grpc/health` | (same) | gRPC health check protocol |

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `LEDGER_GRPC_PORT` | `50051` | gRPC listen port |
| `LEDGER_METRICS_PORT` | `9101` | Prometheus + healthz port |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB` | see .env | Database connection |
| `REDIS_ADDR` | `localhost:6379` | Idempotency store |

---

## Phase 6 — account-svc

### What it does
Wallet service. Manages account balances and holds (reserved funds). Balances are stored directly (unlike ledger-svc) because account-svc owns the source-of-truth for available and held funds. Supports hold-and-settle (PlaceHold → DebitAccount via hold_id) for T+2 payment flows, plus direct credit/debit called by transaction-svc during saga commit.

### Service ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `50052` | gRPC | AccountService RPCs |
| `9102` | HTTP | `/metrics` (Prometheus) + `/healthz` |

### gRPC RPCs

| RPC | What it does |
|-----|-------------|
| `CreateAccount` | Creates wallet row. Idempotency via Redis SetNX. |
| `GetAccount` | Returns account by ID |
| `ListAccounts` | Lists accounts for an owner (cursor pagination by offset) |
| `PlaceHold` | Reserves funds: moves amount from `balance` → `hold_balance`, inserts hold row |
| `ReleaseHold` | Cancels hold: moves amount back from `hold_balance` → `balance`, deletes hold row |
| `CreditAccount` | Adds funds (incoming payment). Called by transaction-svc. Idempotent. |
| `DebitAccount` | Subtracts funds. If `hold_id` set: consumes hold from `hold_balance`. Else: direct debit from `balance`. Idempotent. |

### Database schema (account schema)

| Table | Purpose |
|-------|---------|
| `account.accounts` | Wallets: `balance` (available), `hold_balance` (sum of active holds), `status` |
| `account.holds` | Active holds with expiry, amount, reference_id |
| `account.outbox_messages` | CDC outbox for future event publishing |

### Balance invariants

- `balance >= 0` enforced by CHECK constraint
- `hold_balance >= 0` enforced by CHECK constraint
- `balance + hold_balance = total funds` — PlaceHold/ReleaseHold/DebitAccount maintain this atomically in a transaction
- Unique active account per `(owner_id, currency)` via partial unique index on `status = 'ACTIVE'`

### Idempotency keys

| Operation | Key prefix |
|-----------|-----------|
| CreateAccount | `account:create:{idempotency_key}` |
| PlaceHold | `account:hold:{idempotency_key}` |
| CreditAccount | `account:credit:{idempotency_key}` |
| DebitAccount | `account:debit:{idempotency_key}` |

All stored in Redis with 24h TTL (same `internal/idempotency` RedisStore as ledger-svc).

### Files created

```
services/account-svc/
├── cmd/account-svc/main.go         gRPC + metrics server, graceful shutdown
├── internal/
│   ├── config/config.go            env-based config (ports, DSN, Redis addr)
│   ├── db/
│   │   ├── migrate.go              idempotent SQL: accounts + holds + outbox tables
│   │   └── repo.go                 all 7 RPC data operations
│   └── service/
│       └── account.go              AccountServiceServer implementation
└── go.mod
```

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `ACCOUNT_GRPC_PORT` | `50052` | gRPC listen port |
| `ACCOUNT_METRICS_PORT` | `9102` | Prometheus + healthz port |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB` | see .env | Database connection |
| `REDIS_ADDR` | `localhost:6379` | Idempotency store |

### Proto changes (Phase 4 → Phase 6)

Added to `proto/account/v1/account.proto` and regenerated with `buf generate`:
- `CreditAccountRequest` / `CreditAccountResponse`
- `DebitAccountRequest` / `DebitAccountResponse`
- Two new RPCs in `AccountService`: `CreditAccount`, `DebitAccount`

---

## UI — Web Dashboard

### What it does
Admin dashboard for the ledger platform. A React SPA served by a Go BFF (Backend-for-Frontend) that translates REST → gRPC calls to ledger-svc and account-svc.

### Service ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `8090` | HTTP | BFF — serves React app + REST API |
| `5173` | HTTP | Vite dev server (dev mode only) |

### Pages

| Page | What you can do |
|------|----------------|
| Dashboard | Service health (all 4 services), infra overview, phase progress tracker |
| Accounts | Search accounts by owner ID, create account, credit, debit, place hold, release hold |
| Ledger | Load balance + entries by account ID, post double-entry |
| FX Rates | Get quotes for any currency pair, lock a rate, view seeded rate reference table |
| Transactions | Send payments between accounts, look up transaction status |
| Payouts | Withdraw funds via NACH/SEPA/SWIFT, look up payout status |

### Architecture

```
Browser → :8090 (ui-bff)
              ├─ GET /api/* → gRPC → ledger-svc  :50051
              ├─ GET /api/* → gRPC → account-svc :50052
              └─ GET /*     → static React files (ui/dist/)
```

### Files created

```
ui/
├── package.json                 React 18 + Vite + Tailwind + lucide-react
├── vite.config.js               Proxy /api → :8090 in dev mode
├── tailwind.config.js           Dark finance theme
├── index.html
└── src/
    ├── App.jsx                  Layout + page router
    ├── index.css                Tailwind + shared component classes
    ├── api/client.js            fetch wrapper for all BFF endpoints
    ├── components/
    │   ├── Sidebar.jsx          Navigation (Dashboard, Accounts, Ledger, FX Rates)
    │   ├── Modal.jsx            Reusable modal dialog
    │   └── Toast.jsx            Success/error notifications
    └── pages/
        ├── Dashboard.jsx        Health cards + infra overview + phase tracker
        ├── Accounts.jsx         Full account CRUD + operations
        ├── Ledger.jsx           Balance display + entries table + post entry
        ├── FX.jsx               Quote form, QuoteCard with lock button, rate table
        ├── Transactions.jsx     Send payment form, lookup by ID, saga status cards, "Trace" button
        ├── TransactionTrace.jsx Real-time saga step visualizer (polls /api/transactions/{id}/trace)
        └── Payouts.jsx          Initiate payout form (NACH/SEPA/SWIFT), lookup by ID

services/ui-bff/
├── cmd/ui-bff/main.go           HTTP server + gRPC client setup (ledger, account, fx, txn)
├── internal/handler/
│   ├── router.go                ServeMux routes + protojson serialization
│   ├── accounts.go              Account REST handlers
│   ├── ledger.go                Ledger REST handlers
│   ├── fx.go                    FX REST handlers (getQuote, lockQuote)
│   ├── transaction.go           Transaction REST handlers (initiatePayment, getTransaction, getTransactionTrace)
│   ├── payout.go                Payout REST handlers (initiatePayout, getPayout)
│   └── health.go                /api/health — polls all 6 service /healthz endpoints
└── go.mod
```

### Key dependencies

| Package | Purpose |
|---------|---------|
| `lucide-react` | Icons |
| `tailwindcss` v3 | Styling |
| `@vitejs/plugin-react` | Vite React plugin |
| `protojson` (Go) | Proto → JSON with named enum strings + RFC3339 timestamps |

### Notes
- BFF uses `protojson.MarshalOptions{UseProtoNames: true}` so enum values appear as strings (e.g. `"ACCOUNT_STATUS_ACTIVE"`) not integers
- Vite proxies `/api` to `:8090` in dev mode — no CORS issues during development
- React app is compiled to `ui/dist/` and served as static files by the BFF in production
- `GET /api/transactions/{id}/trace` is proxied from BFF to transaction-svc's admin HTTP server at `TXN_ADMIN_URL` (default `http://localhost:9104`) — no proto change needed

### Environment variables (ui-bff)

| Variable | Default | Purpose |
|----------|---------|---------|
| `UI_BFF_ADDR` | `:8090` | HTTP listen address |
| `TXN_ADMIN_URL` | `http://localhost:9104` | transaction-svc admin HTTP (trace API) |
| `TRANSACTION_GRPC_ADDR` | `localhost:50054` | transaction-svc gRPC |

---

## Phase 7 — fx-svc

### What it does
Foreign exchange rate service. Stores seeded exchange rates in Postgres, issues short-lived quotes (30s TTL), and locks a rate atomically using `SELECT FOR UPDATE` to prevent double-use. Missing direct rates are resolved via inverse lookup (e.g. INR→USD derived from 1/USD→INR). Idempotent lock replay using Redis.

### Service ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `50053` | gRPC | FXService RPCs |
| `9103` | HTTP | `/metrics` (Prometheus) + `/healthz` |

### gRPC RPCs

| RPC | What it does |
|-----|-------------|
| `GetQuote` | Looks up rate (direct or inverse), creates quote row with 30s expiry |
| `LockQuote` | Atomically locks an active quote via SELECT FOR UPDATE. Idempotent replay. |

### Database schema (fx schema)

| Table | Purpose |
|-------|---------|
| `fx.rates` | Rate rows: `from_cur`, `to_cur`, `rate`, `valid_until`. UNIQUE(from_cur, to_cur) |
| `fx.quotes` | Quote rows: `from_currency`, `to_currency`, `rate`, `source_amount`, `target_amount`, `locked`, `locked_by`, `expires_at` |

### Seeded rates (14 pairs)

| Pair | Rate | Pair | Rate |
|------|------|------|------|
| USD → INR | 83.5000 | INR → USD | 0.01198 |
| USD → EUR | 0.9200 | EUR → USD | 1.0870 |
| USD → GBP | 0.7860 | GBP → USD | 1.2722 |
| EUR → INR | 90.7200 | GBP → INR | 106.100 |
| USD → SGD | 1.3510 | SGD → USD | 0.7402 |
| USD → AED | 3.6730 | AED → USD | 0.2722 |
| INR → EUR | 0.01102 | INR → GBP | 0.00943 |

### Rate resolution strategy

1. Query `fx.rates` for direct pair (from_cur, to_cur)
2. If not found, query inverse pair (to_cur, from_cur) and compute `1 / inverse_rate`
3. If neither found → `NOT_FOUND` error

### Quote lifecycle

```
GetQuote → creates quote row (locked=false, expires_at=now+30s)
LockQuote → SELECT FOR UPDATE on quote row
    ├─ if expired           → returns locked: false
    ├─ if already locked    →
    │      if same locked_by → idempotent replay → locked: true
    │      else              → locked: false (double-lock blocked)
    └─ if active + unlocked → SET locked=true, locked_by=key → locked: true
```

### Files created

```
services/fx-svc/
├── cmd/fx-svc/main.go           gRPC + metrics server, graceful shutdown
├── internal/
│   ├── config/config.go         env-based config (ports, DSN, Redis addr)
│   ├── db/
│   │   ├── migrate.go           idempotent SQL: fx.rates + fx.quotes DDL
│   │   ├── seed.go              14 default rate pairs (ON CONFLICT DO UPDATE)
│   │   └── repo.go              GetRate, CreateQuote, LockQuote, ListRates
│   └── service/
│       └── fx.go                FXServiceServer implementation
└── go.mod
```

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `FX_GRPC_PORT` | `50053` | gRPC listen port |
| `FX_METRICS_PORT` | `9103` | Prometheus + healthz port |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB` | see .env | Database connection |
| `REDIS_ADDR` | `localhost:6379` | Idempotency store for lock replay |

### Idempotency

| Operation | Key prefix |
|-----------|-----------|
| LockQuote | `fx:lock:{idempotency_key}` |

Stored in Redis with 24h TTL. On replay, the Redis hit short-circuits before hitting Postgres.

### go.work change

`./services/fx-svc` added to workspace.

---

## Phase 8 — transaction-svc

### What it does
Saga orchestrator for payments. Runs a distributed saga across account-svc and ledger-svc: PlaceHold → PostEntry → DebitAccount (via hold) → CreditAccount → COMPLETED + outbox event. Any step failure compensates by releasing the hold and marking FAILED. All operations are idempotent via Redis.

### Service ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `50054` | gRPC | TransactionService RPCs |
| `9104` | HTTP | `/metrics` (Prometheus) + `/healthz` |

### gRPC RPCs

| RPC | What it does |
|-----|-------------|
| `InitiatePayment` | Runs the full saga. Returns COMPLETED or FAILED transaction. Idempotent. |
| `GetTransaction` | Returns transaction by ID |

### Saga steps

```
1. INSERT transaction row (PENDING)
2. account-svc.PlaceHold      → reserves funds on source (sets hold_id)
3. ledger-svc.PostEntry       → writes double-entry accounting record
4. account-svc.DebitAccount   → consumes hold from source (hold_id)
5. account-svc.CreditAccount  → credits destination account
6. UPDATE transactions(COMPLETED) + INSERT outbox(payment.completed)  ← atomic DB txn
```

Compensation on failure (after step 2): account-svc.ReleaseHold + UPDATE transactions(FAILED) + INSERT outbox(payment.failed).

Each step (PlaceHold → PostEntry → DebitAccount → CreditAccount → FinishTransaction) is recorded to `transaction.saga_steps` with seq, status, duration_ms, and error_msg for the trace visualizer.

### Database schema (transaction schema)

| Table | Purpose |
|-------|---------|
| `transaction.transactions` | Saga state: PENDING → COMPLETED / FAILED / REVERSED |
| `transaction.outbox_messages` | `payment.completed` / `payment.failed` events for Debezium → Kafka |
| `transaction.saga_steps` | Per-step trace: step_name, status, duration_ms, error_msg, started_at/finished_at |

### Idempotency

| Operation | Key prefix |
|-----------|-----------|
| InitiatePayment | `txn:initiate:{idempotency_key}` |

Sub-operations use derived keys so upstream services deduplicate them:
- `hold:{txn_id}` → PlaceHold
- `ledger:{txn_id}` → PostEntry
- `debit:{txn_id}` → DebitAccount
- `credit:{txn_id}` → CreditAccount

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `TRANSACTION_GRPC_PORT` | `50054` | gRPC listen port |
| `TRANSACTION_METRICS_PORT` | `9104` | Prometheus + healthz + `GET /trace/{id}` port |
| `LEDGER_GRPC_ADDR` | `localhost:50051` | ledger-svc upstream |
| `ACCOUNT_GRPC_ADDR` | `localhost:50052` | account-svc upstream |
| `POSTGRES_HOST/PORT/USER/PASSWORD/DB` | see .env | Database connection |
| `REDIS_ADDR` | `localhost:6379` | Idempotency store |

### Files created

```
services/transaction-svc/
├── cmd/transaction-svc/main.go          gRPC + metrics server, dials ledger + account
├── internal/
│   ├── config/config.go                 env config (ports, DSN, Redis, upstream addrs)
│   ├── db/
│   │   ├── migrate.go                   transactions + outbox_messages DDL
│   │   └── repo.go                      Create/Get/SetHoldID/FinishTransaction
│   └── service/
│       └── transaction.go               Saga implementation + compensation logic
└── go.mod
```

### go.work change

`./services/transaction-svc` was already in go.work (pre-scaffolded stub).

---

## Phase 9 — payout-svc + recon-worker

### payout-svc — What it does
Withdraws funds from the platform to external bank accounts. Debits the source account, posts a ledger entry (debit account → credit PAYOUT_CLEARINGHOUSE virtual account), generates a rail-specific bank file (NACH/SEPA/SWIFT JSON format) and uploads it to MinIO `settlement-files` bucket. Publishes `payout.initiated` event to outbox.

### payout-svc ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `50055` | gRPC | PayoutService RPCs |
| `9105` | HTTP | `/metrics` + `/healthz` |

### gRPC RPCs

| RPC | What it does |
|-----|-------------|
| `InitiatePayout` | Debits account → PostEntry → generates bank file → uploads to MinIO → PROCESSING + outbox |
| `GetPayout` | Returns payout by ID |

### Payout saga

```
1. INSERT payout (PENDING)
2. account-svc.DebitAccount   → funds leave the platform wallet
3. ledger-svc.PostEntry       → debit account, credit PAYOUT_CLEARINGHOUSE
4. Generate bank file (JSON)  → batches/{batch_id}/{payout_id}.json
5. Upload to MinIO            → settlement-files bucket
6. UPDATE payout(PROCESSING, batch_id, file_key) + INSERT outbox(payout.initiated)  ← atomic
```

### Database schema (payout schema)

| Table | Purpose |
|-------|---------|
| `payout.payouts` | Payout rows: status PENDING→PROCESSING→SETTLED/FAILED, batch_id, settlement_file key |
| `payout.outbox_messages` | `payout.initiated` / `payout.failed` events for Debezium → Kafka |

### Bank file format (JSON, MinIO `settlement-files/batches/{batch_id}/{payout_id}.json`)

```json
{
  "batch_id": "uuid",
  "rail": "NACH",
  "generated_at": "RFC3339",
  "sponsor_bank": "LEDGER001",
  "records": [{ "payout_id", "account_number", "ifsc_code", "amount", "currency", ... }]
}
```

### Rails supported

| Rail | Use case | Required fields |
|------|----------|----------------|
| `NACH` | India bank transfer | account_number, ifsc_code |
| `SEPA` | Europe bank transfer | iban, bic |
| `SWIFT` | International wire | iban, bic |

### payout-svc environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `PAYOUT_GRPC_PORT` | `50055` | gRPC listen port |
| `PAYOUT_METRICS_PORT` | `9105` | Prometheus + healthz |
| `LEDGER_GRPC_ADDR` | `localhost:50051` | ledger-svc upstream |
| `ACCOUNT_GRPC_ADDR` | `localhost:50052` | account-svc upstream |
| `MINIO_ENDPOINT` | `localhost:9000` | MinIO S3-compatible API |
| `MINIO_ACCESS_KEY` | `minioadmin` | MinIO credentials |
| `MINIO_SECRET_KEY` | `minioadmin` | MinIO credentials |
| `MINIO_SETTLEMENT_BUCKET` | `settlement-files` | Bucket for bank files |

---

### recon-worker — What it does
Background ticker that compares `account.accounts.balance` against the projected balance from `ledger.entries` for each account. Reports drift accounts (balance ≠ ledger sum), classifies them as `covered` (has ledger entries but balance differs) or `uncovered` (no ledger entries). Uploads JSON report to MinIO `recon-reports` bucket on every run.

### recon-worker port

| Port | Purpose |
|------|---------|
| `9106` | `/metrics` + `/healthz` |

### Drift classification

| Coverage | Meaning |
|----------|---------|
| `covered` | Account has ledger entries — balance mismatch is a real drift requiring investigation |
| `uncovered` | No ledger entries — account funded entirely via direct REST credits (expected in dev/test) |

### Recon SQL

Compares `account.accounts.balance` with `SUM(CASE WHEN type='CREDIT' THEN amount ELSE -amount END)` from `ledger.entries` grouped by `account_id`. Cross-schema JOIN: `ledger.entries.account_id (TEXT) = account.accounts.id::text`.

### recon-worker environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `RECON_METRICS_PORT` | `9106` | Prometheus + healthz |
| `RECON_INTERVAL_SECONDS` | `60` | Seconds between recon runs |
| `MINIO_ENDPOINT` | `localhost:9000` | MinIO endpoint |
| `MINIO_RECON_BUCKET` | `recon-reports` | Bucket for recon reports |

### Files created

```
services/payout-svc/
├── cmd/payout-svc/main.go          gRPC + metrics server, dials ledger + account + MinIO
├── internal/
│   ├── config/config.go
│   ├── db/
│   │   ├── migrate.go              payout.payouts + payout.outbox_messages DDL
│   │   └── repo.go                 Create/Get/ProcessPayout/FailPayout
│   ├── storage/minio.go            MinIO Upload wrapper
│   └── service/payout.go           Saga + bank file generation
└── go.mod

services/recon-worker/
├── cmd/recon-worker/main.go        Ticker + metrics HTTP server
├── internal/
│   ├── config/config.go
│   └── worker/recon.go             findDrift SQL + MinIO report upload
└── go.mod
```

---

## Phase 10 — API Gateway

### Purpose

Public-facing HTTP edge that sits in front of ui-bff. All external traffic enters through the gateway. Provides:
- **JWT authentication** — HS256 tokens issued at `/auth/login`, validated on every `/api/*` call
- **Per-IP rate limiting** — token bucket (300 req/min default), `429 Retry-After: 60` on breach
- **Request ID injection** — `X-Request-ID` header added/propagated on every request
- **CORS** — `Access-Control-Allow-*` headers for browser clients
- **Access logging** — method, path, status, latency via zerolog
- **Reverse proxy** — `net/http/httputil.ReverseProxy` forwards to `UPSTREAM_URL` (ui-bff :8090)

### Port map

| Port | Purpose |
|------|---------|
| 8080 | Public HTTP — login + all API traffic |
| 9107 | Prometheus metrics + `/healthz` sidecar |

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `GATEWAY_PORT` | `8080` | Public listen port |
| `GATEWAY_METRICS_PORT` | `9107` | Metrics + healthz port |
| `UPSTREAM_URL` | `http://localhost:8090` | ui-bff address |
| `JWT_SECRET` | *(must set)* | HMAC-SHA256 signing key |
| `ADMIN_USERNAME` | `admin` | Login username |
| `ADMIN_PASSWORD` | `admin` | Login password |
| `RATE_LIMIT_RPM` | `300` | Requests per minute per IP |

### Auth flow

1. Client POSTs `{"username":"admin","password":"admin"}` → `/auth/login`
2. Gateway returns `{"token":"eyJ...","expires_in":3600}`
3. Client stores token in `sessionStorage` (React app)
4. All subsequent requests carry `Authorization: Bearer <token>`
5. Gateway verifies signature + expiry; injects `X-Auth-Subject`, `X-Auth-Role` headers
6. `Authorization` header is stripped before forwarding to ui-bff

### Public endpoints (no auth required)

| Path | Method | Purpose |
|------|--------|---------|
| `/auth/login` | POST | Issue JWT |
| `/auth/refresh` | POST | Re-issue JWT (existing valid token required) |
| `/healthz` | GET | Proxied to ui-bff healthz |

### Files created

```
services/gateway/
├── cmd/gateway/main.go             HTTP server :8080 + metrics :9107, middleware chain
├── internal/
│   ├── auth/jwt.go                 HS256 sign/verify, Claims{Subject, Role, exp}
│   ├── middleware/
│   │   ├── auth.go                 Bearer token validation, public path skip list
│   │   ├── cors.go                 CORS preflight + headers
│   │   ├── logger.go               Zerolog access log (method, path, status, latency)
│   │   ├── ratelimit.go            Per-IP token bucket, 5-min cleanup goroutine
│   │   ├── requestid.go            X-Request-ID propagation
│   │   └── util.go                 genID(), writeJSON()
│   ├── proxy/proxy.go              httputil.ReverseProxy — strips Auth, sets X-Forwarded-Host
│   └── handler/auth.go             /auth/login + /auth/refresh handlers
└── go.mod
```

### Middleware chain (outermost → innermost)

```
CORS → RequestID → Logger → RateLimiter → Auth → Mux (proxy / auth handlers)
```

### React UI changes

- **`ui/src/pages/Login.jsx`** — New login form, POSTs to `/auth/login`, stores token in `sessionStorage`
- **`ui/src/App.jsx`** — Reads `sessionStorage.jwt` on load; renders Login if absent; adds "Sign out" button in top bar
- **`ui/src/api/client.js`** — `authHeader()` reads `sessionStorage.jwt` and injects `Authorization: Bearer` on every API call
- **`ui/src/components/Sidebar.jsx`** — Footer now shows Phase 10 · gateway-svc

### Go dependencies added (gateway module)

| Package | Version | Why |
|---------|---------|-----|
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | HS256 JWT sign/verify |
| `golang.org/x/time` | v0.15.0 | `rate.Limiter` token bucket |
| `github.com/rs/zerolog` | v1.35.1 | Structured logging |
| `github.com/prometheus/client_golang` | v1.20.5 | Prometheus metrics |

---

## Phase 11 — Kafka Consumers

### Purpose

Event-driven processing layer. Consumes events from Redpanda and drives three downstream side-effects: audit logging, analytics counters, and notification logging. Also contains an **outbox relay** that polls Postgres outbox tables and publishes to Kafka (replaces Debezium in local dev).

### Architecture

```
Postgres outbox tables                    Redpanda topics
 transaction.outbox_messages  ──relay──►  payments.payment.completed
 payout.outbox_messages       ──relay──►  payments.payment.failed
                                          payments.payout.initiated
                                          payments.recon.drift
                                          payments.hold.matured
                                                │
                                         consumer group (ledger-consumers)
                                                │
                              ┌─────────────────┼──────────────────┐
                         audit handler    analytics handler   notification handler
                              │                 │                    │
                    audit.events (Postgres)  analytics.event_counts  zerolog
```

### Outbox Relay

Polls `transaction.outbox_messages` and `payout.outbox_messages` every `RELAY_INTERVAL_SECONDS` (default 5s). Uses `SELECT ... FOR UPDATE SKIP LOCKED` + `DELETE` in a CTE for at-least-once delivery. Maps `event_type` to Kafka topic via a static lookup table. In production, Debezium replaces this relay.

### Kafka Consumer

- Client: `github.com/twmb/franz-go v1.18.1` (pure Go, no librdkafka)
- Consumer group: `ledger-consumers` (offset auto-commit disabled; commits after each batch)
- Topics: `payments.payment.completed`, `payments.payment.failed`, `payments.payout.initiated`, `payments.payout.finalized`, `payments.hold.matured`, `payments.recon.drift`

### Handlers

| Handler | Input | Output |
|---------|-------|--------|
| audit | every event | INSERT into `audit.events` (topic, event_type, aggregate_id, payload) |
| analytics | every event | UPSERT daily counter in `analytics.event_counts` |
| notification | payment.completed/failed, payout.initiated, recon.drift | zerolog INFO/WARN/ERROR log |

### Database tables

```sql
CREATE TABLE audit.events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic TEXT NOT NULL, event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL, aggregate_type TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}',
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE analytics.event_counts (
    event_type TEXT NOT NULL, period DATE NOT NULL DEFAULT CURRENT_DATE,
    count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (event_type, period)
);
```

### Port map

| Port | Purpose |
|------|---------|
| 9108 | Prometheus metrics + `/healthz` |

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `DATABASE_URL` | postgres://ledger:ledger@localhost:5432/ledger | Postgres connection |
| `KAFKA_BROKERS` | `localhost:19092` | Redpanda broker address |
| `KAFKA_GROUP_ID` | `ledger-consumers` | Consumer group ID |
| `CONSUMERS_METRICS_PORT` | `9108` | Metrics port |
| `RELAY_INTERVAL_SECONDS` | `5` | Outbox poll interval |

### Files created

```
services/consumers/
├── cmd/consumers/main.go               Wires pool, relay, consumer, metrics
├── internal/
│   ├── config/config.go
│   ├── db/
│   │   ├── migrate.go                  audit + analytics schema DDL
│   │   └── repo.go                     InsertAuditEvent, IncrAnalyticsCounter, PollOutbox
│   ├── relay/outbox.go                 Outbox poller → Kafka producer
│   ├── consumer/consumer.go            franz-go consumer group loop
│   └── handler/handler.go              Dispatch to audit/analytics/notification
└── go.mod
```

---

## Phase 12 — Tests

### Coverage

| Module | Test file(s) | What is tested |
|--------|-------------|----------------|
| `internal/errors` | errors_test.go | Error wrapping, gRPC code extraction |
| `internal/idempotency` | idempotency_test.go | Get cache miss, Set NX semantics |
| `internal/logging` | logging_test.go | JSON output, pretty mode |
| `internal/metrics` | metrics_test.go | Counter increment, histogram observe |
| `internal/money` | money_test.go | Decimal add/sub, rounding, currency checks |
| `internal/outbox` | outbox_test.go | Payload marshal/unmarshal |
| `internal/tracing` | tracing_test.go | Span creation and propagation |
| `services/gateway/internal/auth` | jwt_test.go | Sign+Verify round-trip, expired token, wrong secret, tampered payload, empty token |
| `services/gateway/internal/middleware` | auth_test.go | Non-API paths bypass auth, API paths require JWT, expired/wrong-secret → 401, X-Auth-Subject/Role headers forwarded |
| `services/gateway/internal/middleware` | ratelimit_test.go | Under-limit → 200, over-limit → 429, per-IP independence, Retry-After header |
| `services/gateway/internal/middleware` | cors_test.go | CORS headers set, OPTIONS preflight → 204 |
| `services/transaction-svc/internal/service` | transaction_test.go | Missing idempotency key, missing accounts, missing/empty amount, same source+dest, empty currency, missing GetTransaction ID |
| `services/payout-svc/internal/service` | payout_test.go | Missing key/account/amount/destination, negative amount (also fixed service bug), missing GetPayout ID |

### Bugs fixed during Phase 12

- **payout-svc**: `InitiatePayout` accepted negative amounts (e.g. `-100`) because only `decimal.NewFromString` was checked, not `amt.IsNegative()`. Fixed: reject if `amt.IsNegative() || amt.IsZero()`.

### Test runner

```bash
# scripts/test-all.sh — runs go test -race -count=1 ./... in every module
./scripts/test-all.sh
```

All 10 modules: `internal`, `ledger-svc`, `account-svc`, `fx-svc`, `transaction-svc`, `payout-svc`, `recon-worker`, `gateway`, `consumers`, `ui-bff` — **ALL TESTS PASSED**.

---

## Phase 13 — Helm + CI/CD

### Dockerfiles — one per service

All Dockerfiles use multi-stage builds. gRPC services (ledger, account, fx, transaction, payout) and workers (recon, consumers) use 2 stages. ui-bff and gateway use 3 stages (Node → Go → distroless).

**Pattern (Go-only services):**
```
Stage 1 (golang:1.26-alpine)
  - COPY go.work + gen/ + internal/ + services/{svc}/
  - go work edit -dropuse for all other modules
  - go build -ldflags="-s -w" -trimpath

Stage 2 (distroless/static-debian12)
  - COPY binary from stage 1
  - ~25 MB final image
```

**Pattern (ui-bff and gateway — 3 stages):**
```
Stage 1 (node:18-alpine)
  - npm ci + npm run build → dist/

Stage 2 (golang:1.26-alpine)
  - go build as above

Stage 3 (distroless/static-debian12)
  - COPY binary + dist/ files
  - ENV STATIC_DIR=/ui/dist
```

**Key Dockerfile design decision:** `go.work` lists all 9 service modules. Each Dockerfile only copies its own service directory, so it uses `go work edit -dropuse=./services/X` to strip unused module references before `go build`.

### Helm chart — `helm/ledger/`

Single umbrella chart using `range` over `values.services` map. No per-service chart files needed.

**Resources rendered (per `helm template`):**
- 9 × `Deployment` — one per service, with liveness/readiness probes on `/healthz:{metricsPort}`
- 9 × `Service` — ClusterIP, with `main` and `metrics` ports
- 1 × `Secret` — `ledger-secrets` (postgres-url, redis-url, minio credentials)
- 1 × `Ingress` — disabled by default, routes `gateway:8080` to host
- N × `ServiceMonitor` — disabled by default, for Prometheus Operator

**Template files:**
```
helm/ledger/
├── Chart.yaml
├── values.yaml             services map + global infra config
└── templates/
    ├── _helpers.tpl        ledger.name, ledger.image, ledger.labels
    ├── deployments.yaml    range over services → Deployment per entry
    ├── services.yaml       range over services → Service per entry
    ├── secrets.yaml        ledger-secrets Secret
    ├── ingress.yaml        Ingress (enabled: false by default)
    └── servicemonitor.yaml ServiceMonitor (enabled: false by default)
```

**Secrets injection pattern:** Sensitive values (postgres URL, minio key) live in the `ledger-secrets` Secret. Each Deployment reads them via `secretKeyRef`. Service-specific env vars (gRPC addresses, ports) are rendered directly from `values.services.{name}.env`.

### GitHub Actions

**`ci.yml`** — runs on every push and PR:
1. `go vet ./...` in every module
2. `./scripts/test-all.sh` (race detector)
3. Build all 9 binaries (verify compile)
4. `helm lint` + dry-run `helm template`

**`cd.yml`** — runs on push to `main`:
1. Build 9 Docker images in parallel (matrix strategy)
2. Push to `ghcr.io/{owner}/ledger/{service}:{sha}` and `:latest`
3. `helm upgrade --install ledger` (if `DEPLOY_ENABLED=true` repo variable is set)
4. `kubectl rollout status` for key deployments

**Image tags:** `ghcr.io/{repo}/{service}:{7-char-sha}` (every commit) + `:latest` (main branch only). Uses `docker/metadata-action` for tag generation.

**Docker layer caching:** `cache-from: type=gha` / `cache-to: type=gha,mode=max` — GitHub Actions cache backend, persists across workflow runs.

### Files created

```
services/{each-service}/Dockerfile    9 Dockerfiles
helm/ledger/
  Chart.yaml
  values.yaml
  templates/
    _helpers.tpl
    deployments.yaml
    services.yaml
    secrets.yaml
    ingress.yaml
    servicemonitor.yaml
.github/workflows/
  ci.yml
  cd.yml
```

---

## UI Overhaul (post Phase 13)

### Files changed

| File | What changed |
|------|--------------|
| `ui/src/components/Sidebar.jsx` | Phase footer removed; status indicator added |
| `ui/src/pages/Login.jsx` | Phase label removed; credential hint added |
| `ui/src/pages/Dashboard.jsx` | Full rewrite — quick actions, live health, flow guide |
| `ui/src/App.jsx` | Passes `onNavigate` to all pages |
| `ui/src/pages/Accounts.jsx` | Inline ledger entries per account card |
| `ui/src/pages/Transactions.jsx` | Auto-polls pending transactions every 3 s; "Trace" button navigates to saga visualizer |
| `ui/src/pages/TransactionTrace.jsx` | Saga step timeline; polls every 2 s while PENDING; shows duration_ms per step |
| `ui/src/pages/Payouts.jsx` | Auto-polls processing payouts every 5 s |

### Dynamic features

| Feature | Implementation |
|---------|---------------|
| Quick actions | Dashboard cards call `onNavigate(pageId)` from App state |
| Inline entries | `api.listEntries(acc.id, 20)` on button click, result stored in AccountCard local state |
| Pending auto-poll | `useEffect` with `setTimeout(3000)` fires when `txns` contains PENDING items |
| Processing auto-poll | Same pattern, `setTimeout(5000)` for PROCESSING/PENDING payouts |
| Copy ID | `navigator.clipboard.writeText(id)` on Copy button in TxnCard / PayoutCard |
