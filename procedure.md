# Ledger Platform — Execution Procedure

Commands to run for each phase, in order.
Run these exactly as written from the repo root unless told otherwise.

---

## Phase 1 — Repo Scaffold + Shared Libraries

```bash
# Verify Go is installed
go version

# Download all module dependencies across the workspace
go work sync

# Run go mod tidy in the internal module
cd internal && go mod tidy && cd ..

# Run all tests (no race detector — quick check)
cd internal && go test ./... && cd ..

# Run all tests WITH race detector (use this before committing)
cd internal && go test -race -count=1 ./... && cd ..
```

**Expected output — all tests pass:**
```
ok  github.com/prasham/ledger/internal/errors
ok  github.com/prasham/ledger/internal/idempotency
ok  github.com/prasham/ledger/internal/logging
ok  github.com/prasham/ledger/internal/money
ok  github.com/prasham/ledger/internal/outbox
ok  github.com/prasham/ledger/internal/tracing
```

---

## Phase 2 — Docker Compose Infrastructure

```bash
# Copy env file (first time only)
cp .env.example .env

# Pull all images
docker compose pull

# Start all containers in background
docker compose up -d

# Watch container health status
docker compose ps

# Follow logs of all containers
docker compose logs -f

# Follow logs of a specific container
docker compose logs -f ledger-postgres
docker compose logs -f ledger-debezium
docker compose logs -f ledger-redpanda

# Stop all containers (data volumes preserved)
docker compose down

# Full wipe — stop + delete all volumes (fresh start)
docker compose down -v
```

### Verify each service is healthy

```bash
# PostgreSQL — ping and list schemas
docker exec ledger-postgres pg_isready -U ledger -d ledger
docker exec ledger-postgres psql -U ledger -d ledger -c "\dn"

# Redis — ping
docker exec ledger-redis redis-cli ping

# Redpanda — list topics
docker exec ledger-redpanda rpk topic list --brokers localhost:9092

# Apicurio — health check
curl -s http://localhost:8082/health/ready | python3 -m json.tool

# MinIO — health check
curl -s http://localhost:9000/minio/health/live && echo " healthy"

# Debezium — list connectors
curl -s http://localhost:8083/connectors | python3 -m json.tool

# Debezium — connector status (connector AND task should both show RUNNING)
curl -s http://localhost:8083/connectors/ledger-outbox-connector/status | python3 -m json.tool

# If Debezium task is FAILED with "publication already active" error:
# The connector config had publication.autocreate.mode=filtered but Postgres already has
# a FOR ALL TABLES publication — fix by deleting and re-registering with all_tables mode.
curl -s -X DELETE http://localhost:8083/connectors/ledger-outbox-connector
docker compose run --rm debezium-init

# Toxiproxy — list proxies
curl -s http://localhost:8474/proxies | python3 -m json.tool
```

### Browser UIs

| UI | URL | Login |
|----|-----|-------|
| Redpanda Console | http://localhost:8085 | none |
| MinIO Console | http://localhost:9001 | minioadmin / minioadmin |
| Apicurio Registry | http://localhost:8082/ui | none |
| pgAdmin 4 | http://localhost:5050 | admin@ledger.dev / admin |
| RedisInsight | http://localhost:5540 | none (first launch) |

```bash
# pgAdmin — after logging in, add a server manually:
#   General > Name:     ledger
#   Connection > Host:  ledger-postgres
#   Connection > Port:  5432
#   Connection > User:  ledger
#   Connection > Pass:  ledger
#   Connection > DB:    ledger

# RedisInsight — after opening, add a Redis database:
#   Host: ledger-redis
#   Port: 6379
#   (no password)

# Start only the UI containers (if already have infra running)
docker compose up -d pgadmin redisinsight

# Check they are running
docker compose ps pgadmin redisinsight
```

### End-to-end outbox → Kafka test

```bash
# Step 1: Create outbox table (temporary — real migrations come in Phase 5)
docker exec ledger-postgres psql -U ledger -d ledger -c "
  CREATE TABLE IF NOT EXISTS transaction.outbox_messages (
    id            UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    aggregate_id  TEXT        NOT NULL,
    aggregate_type TEXT       NOT NULL,
    event_type    TEXT        NOT NULL,
    payload       JSONB       NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW()
  );"

# Step 2: Insert a fake payment event (simulates what transaction-svc does)
docker exec ledger-postgres psql -U ledger -d ledger -c "
  INSERT INTO transaction.outbox_messages
    (aggregate_id, aggregate_type, event_type, payload)
  VALUES
    ('account-uuid-123', 'payment', 'payment.completed',
     '{\"transaction_id\":\"txn-001\",\"amount\":\"500.0000\",\"currency\":\"INR\"}')
  RETURNING id, event_type;"

# Step 3: Read it from Kafka — Debezium should have forwarded it
docker exec ledger-redpanda rpk topic consume payments.payment.completed \
  --brokers localhost:9092 --num 1 --offset start
```

### Chaos testing with Toxiproxy

```bash
# Inject 500ms latency on Postgres connection
curl -s -X POST http://localhost:8474/proxies/postgres/toxics \
  -H "Content-Type: application/json" \
  -d '{"name":"slow","type":"latency","attributes":{"latency":500}}'

# Remove the toxic (restore normal)
curl -s -X DELETE http://localhost:8474/proxies/postgres/toxics/slow

# Take Redis offline entirely
curl -s -X POST http://localhost:8474/proxies/redis \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'

# Restore Redis
curl -s -X POST http://localhost:8474/proxies/redis \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

---

## Phase 3 — Observability Stack

> Commands will be added when Phase 3 is built.

---

## Phase 4 — Proto Contracts + buf

> Commands will be added when Phase 4 is built.

---

## Phase 5 — ledger-svc

> Commands will be added when Phase 5 is built.

---

## Phase 6 — account-svc

> Commands will be added when Phase 6 is built.

---

## Phase 7 — fx-svc

> Commands will be added when Phase 7 is built.

---

## Phase 8 — transaction-svc

> Commands will be added when Phase 8 is built.

---

## Phase 9 — payout-svc + recon-worker

> Commands will be added when Phase 9 is built.

---

## Phase 10 — API Gateway

> Commands will be added when Phase 10 is built.

---

## Phase 11 — Kafka Consumers

> Commands will be added when Phase 11 is built.

---

## Phase 12 — Tests

> Commands will be added when Phase 12 is built.

---

## Phase 13 — Helm + CI/CD

> Commands will be added when Phase 13 is built.
