# Ledger Platform — Technical Flow Reference

Everything installed, created, or configured in this project — versions, files, ports, credentials — phase by phase.

---

## Phase 1 — Repo Scaffold + Shared Libraries

### Runtime

| Tool | Version |
|------|---------|
| Go | 1.26.1 (minimum required: 1.22) |

### Go Workspace

| File | Purpose |
|------|---------|
| `go.work` | Workspace file — links all modules locally without publishing |
| `go.work.sum` | Workspace checksum file (auto-generated) |

### Modules created

| Module path | Location | Purpose |
|-------------|----------|---------|
| `github.com/prasham/ledger/internal` | `internal/` | Shared libraries used by all services |
| `github.com/prasham/ledger/services/ledger-svc` | `services/ledger-svc/` | Stub — built in Phase 5 |
| `github.com/prasham/ledger/services/account-svc` | `services/account-svc/` | Stub — built in Phase 6 |
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

> To be filled when Phase 3 is built.

---

## Phase 4 — Proto Contracts + buf

> To be filled when Phase 4 is built.

---

## Phase 5 — ledger-svc

> To be filled when Phase 5 is built.

---

## Phase 6 — account-svc

> To be filled when Phase 6 is built.

---

## Phase 7 — fx-svc

> To be filled when Phase 7 is built.

---

## Phase 8 — transaction-svc

> To be filled when Phase 8 is built.

---

## Phase 9 — payout-svc + recon-worker

> To be filled when Phase 9 is built.

---

## Phase 10 — API Gateway

> To be filled when Phase 10 is built.

---

## Phase 11 — Kafka Consumers

> To be filled when Phase 11 is built.

---

## Phase 12 — Tests

> To be filled when Phase 12 is built.

---

## Phase 13 — Helm + CI/CD

> To be filled when Phase 13 is built.
