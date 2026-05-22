# Ledger Platform — Execution Procedure

Commands to run for each phase, in order.
Run these exactly as written from the repo root unless told otherwise.

---

## Quick Start (Phases 1–13 + UI already built)

```bash
# 1. Start all Docker infrastructure
docker compose up -d

# 2. Start Go services (each in its own terminal or background)
./bin/ledger-svc &
./bin/account-svc &
./bin/fx-svc &
./bin/transaction-svc &
./bin/payout-svc &
./bin/recon-worker &
./bin/ui-bff &
./bin/gateway &

# 3. Open the dashboard (via gateway — requires login)
# http://localhost:8080
# Default credentials: admin / admin  (set ADMIN_USERNAME, ADMIN_PASSWORD in .env)
```

### All service ports at a glance

| Service | Port | Type |
|---------|------|------|
| ledger-svc | 50051 | gRPC |
| ledger-svc | 9101 | HTTP metrics + healthz |
| account-svc | 50052 | gRPC |
| account-svc | 9102 | HTTP metrics + healthz |
| fx-svc | 50053 | gRPC |
| fx-svc | 9103 | HTTP metrics + healthz |
| transaction-svc | 50054 | gRPC |
| transaction-svc | 9104 | HTTP metrics + healthz + `/trace/{id}` |
| payout-svc | 50055 | gRPC |
| payout-svc | 9105 | HTTP metrics + healthz |
| recon-worker | — | no gRPC |
| recon-worker | 9106 | HTTP metrics + healthz |
| gateway | — | no gRPC |
| gateway | 8080 | HTTP public (JWT auth + reverse proxy) |
| gateway | 9107 | HTTP metrics + healthz |
| ui-bff | 8090 | HTTP (REST API + React app, internal) |
| PostgreSQL | 5432 | — |
| Redis | 6379 | — |
| Redpanda | 19092 | Kafka (host) |
| Grafana | 3000 | — |
| Prometheus | 9090 | — |

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

```bash
# Pull images
docker compose pull otel-collector prometheus tempo loki promtail grafana

# Start in dependency order (Tempo + Loki + Prometheus first, then the rest)
docker compose up -d tempo loki prometheus
docker compose up -d otel-collector promtail grafana

# Run internal/metrics tests
cd internal && go test -race -count=1 ./metrics/... && cd ..
```

### Verify each service is healthy

```bash
# Prometheus — health + list scrape targets
curl -s http://localhost:9090/-/healthy && echo " healthy"
curl -s http://localhost:9090/api/v1/targets | python3 -m json.tool

# Tempo — ready check
curl -s http://localhost:3200/ready

# Loki — ready check
curl -s http://localhost:3100/ready

# OTel Collector — health_check extension (internal port, not host-exposed)
# Check logs instead:
docker logs ledger-otel-collector 2>&1 | grep -i "ready\|error"

# Grafana — API health
curl -s http://localhost:3000/api/health | python3 -m json.tool

# Grafana — list provisioned datasources (should show Prometheus, Loki, Tempo)
curl -s -u admin:admin http://localhost:3000/api/datasources | python3 -m json.tool
```

### Send a test trace (verify OTel → Tempo pipeline)

```bash
# Install grpcurl if needed: go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
# Or use the Go test in Phase 5 — the pipeline is verified end-to-end there.
# Quick smoke test: check Tempo has an endpoint ready
curl -s http://localhost:3200/api/echo
```

### Browser UIs

| UI | URL | Login |
|----|-----|-------|
| Grafana | http://localhost:3000 | admin / admin |
| Prometheus | http://localhost:9090 | none |

---

## Phase 4 — Proto Contracts + buf

```bash
# Install tools (one-time)
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Verify
buf --version
protoc-gen-go --version
protoc-gen-go-grpc --version

# Lint proto files
make proto-lint
# or directly:
buf lint proto

# Regenerate Go code (run after every .proto change)
make proto-gen
# or directly:
buf generate

# Verify generated code compiles
go build ./gen/...

# Tidy the gen module after regeneration
cd gen && go mod tidy && cd ..
go work sync
```

### Expected output after buf generate

```
gen/
├── common/v1/money.pb.go
├── account/v1/account.pb.go  + account_grpc.pb.go
├── fx/v1/fx.pb.go            + fx_grpc.pb.go
├── ledger/v1/ledger.pb.go    + ledger_grpc.pb.go
├── payout/v1/payout.pb.go    + payout_grpc.pb.go
└── transaction/v1/transaction.pb.go + transaction_grpc.pb.go
```

---

## Phase 5 — ledger-svc

```bash
# Build
go build -o bin/ledger-svc ./services/ledger-svc/cmd/ledger-svc/

# Run (infra must be up)
LOG_PRETTY=true ./bin/ledger-svc

# Health check
curl http://localhost:9101/healthz

# Metrics
curl http://localhost:9101/metrics
```

### Test RPCs with grpcurl

```bash
# Install grpcurl (one-time)
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# PostEntry — debit sender, credit receiver
grpcurl -plaintext -d '{
  "idempotency_key": "test-001",
  "debit_account_id":  "acc-sender-001",
  "credit_account_id": "acc-receiver-002",
  "amount": {"amount": "500.0000", "currency": "INR"},
  "reference_id": "txn-001",
  "description": "payment test"
}' localhost:50051 ledger.v1.LedgerService/PostEntry

# GetBalance
grpcurl -plaintext -d '{"account_id": "acc-sender-001"}' \
  localhost:50051 ledger.v1.LedgerService/GetBalance

# ListEntries
grpcurl -plaintext -d '{"account_id": "acc-sender-001", "page_size": 10}' \
  localhost:50051 ledger.v1.LedgerService/ListEntries

# Idempotency — replay same key, expect AlreadyExists error
grpcurl -plaintext -d '{
  "idempotency_key": "test-001",
  "debit_account_id":  "acc-sender-001",
  "credit_account_id": "acc-receiver-002",
  "amount": {"amount": "500.0000", "currency": "INR"},
  "reference_id": "txn-001",
  "description": "payment test"
}' localhost:50051 ledger.v1.LedgerService/PostEntry

# Verify entries in Postgres directly
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, account_id, type, amount, currency FROM ledger.entries ORDER BY created_at;"
```

---

## Phase 6 — account-svc

```bash
# Regenerate proto (after account.proto was updated with CreditAccount + DebitAccount)
buf generate

# Build
go build -o bin/account-svc ./services/account-svc/cmd/account-svc/

# Run (infra must be up)
LOG_PRETTY=true ./bin/account-svc

# Health check
curl http://localhost:9102/healthz

# Metrics
curl http://localhost:9102/metrics
```

### Test RPCs with grpcurl

```bash
# CreateAccount
grpcurl -plaintext -d '{
  "idempotency_key": "owner1-inr-001",
  "owner_id": "owner-001",
  "currency": "INR"
}' localhost:50052 account.v1.AccountService/CreateAccount

# GetAccount (use id from CreateAccount response)
grpcurl -plaintext -d '{"account_id": "<id>"}' \
  localhost:50052 account.v1.AccountService/GetAccount

# ListAccounts
grpcurl -plaintext -d '{"owner_id": "owner-001", "page_size": 10}' \
  localhost:50052 account.v1.AccountService/ListAccounts

# CreditAccount (add funds)
grpcurl -plaintext -d '{
  "idempotency_key": "credit-001",
  "account_id": "<id>",
  "amount": {"amount": "1000.0000", "currency": "INR"},
  "reference_id": "txn-incoming-001"
}' localhost:50052 account.v1.AccountService/CreditAccount

# PlaceHold (reserve funds for T+2)
grpcurl -plaintext -d '{
  "idempotency_key": "hold-001",
  "account_id": "<id>",
  "amount": {"amount": "200.0000", "currency": "INR"},
  "reference_id": "txn-outgoing-001",
  "ttl_seconds": 172800
}' localhost:50052 account.v1.AccountService/PlaceHold

# DebitAccount via hold (saga commit — use hold id from PlaceHold response)
grpcurl -plaintext -d '{
  "idempotency_key": "debit-001",
  "account_id": "<id>",
  "amount": {"amount": "200.0000", "currency": "INR"},
  "hold_id": "<hold_id>",
  "reference_id": "txn-outgoing-001"
}' localhost:50052 account.v1.AccountService/DebitAccount

# ReleaseHold (saga rollback — if hold not yet consumed)
grpcurl -plaintext -d '{
  "hold_id": "<hold_id>",
  "account_id": "<id>"
}' localhost:50052 account.v1.AccountService/ReleaseHold

# Idempotency — replay same key, expect AlreadyExists
grpcurl -plaintext -d '{
  "idempotency_key": "credit-001",
  "account_id": "<id>",
  "amount": {"amount": "1000.0000", "currency": "INR"},
  "reference_id": "txn-incoming-001"
}' localhost:50052 account.v1.AccountService/CreditAccount

# Verify in Postgres directly
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, owner_id, currency, status, balance, hold_balance FROM account.accounts;"
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, account_id, amount, reference_id, expires_at FROM account.holds;"
```

---

## UI — Web Dashboard (ui-bff + React)

```bash
# Install Node dependencies (first time only)
cd ui && npm install && cd ..

# Build the React app
cd ui && npm run build && cd ..

# Build the Go BFF
go build -o bin/ui-bff ./services/ui-bff/cmd/ui-bff/

# Start BFF (ledger-svc + account-svc must be running first)
# TXN_ADMIN_URL points to transaction-svc's HTTP admin port (default: http://localhost:9104)
TXN_ADMIN_URL=http://localhost:9104 ./bin/ui-bff

# Open browser
# http://localhost:8090
```

### Dev mode (hot-reload React, proxy API to BFF)

```bash
# Terminal 1 — BFF
./bin/ui-bff

# Terminal 2 — Vite dev server (hot-reload)
cd ui && npm run dev
# Open http://localhost:5173
```

### REST API endpoints (BFF)

```
GET  /api/health
GET  /api/accounts?owner_id=xxx
POST /api/accounts                          body: {idempotency_key, owner_id, currency}
GET  /api/accounts/{id}
POST /api/accounts/{id}/credit              body: {idempotency_key, amount, currency, reference_id}
POST /api/accounts/{id}/debit              body: {idempotency_key, amount, currency, hold_id?, reference_id}
POST /api/accounts/{id}/holds              body: {idempotency_key, amount, currency, reference_id, ttl_seconds?}
DELETE /api/accounts/{id}/holds/{hold_id}
GET  /api/ledger/{account_id}/balance
GET  /api/ledger/{account_id}/entries?page_size=20
POST /api/ledger/entries                   body: {idempotency_key, debit_account_id, credit_account_id, amount, currency, reference_id, description?}
POST /api/fx/quote                         body: {from_currency, to_currency, amount}
POST /api/fx/quote/{id}/lock               body: {idempotency_key}
POST /api/transactions                     body: {idempotency_key, source_account_id, dest_account_id, amount, currency, description?}
GET  /api/transactions/{id}
GET  /api/transactions/{id}/trace          saga step timeline (proxied from transaction-svc admin port 9104)
POST /api/payouts                          body: {idempotency_key, account_id, amount, currency, rail, account_name, account_number?, ifsc_code?, iban?, bic?}
GET  /api/payouts/{id}
```

---

## Phase 7 — fx-svc

```bash
# Build
go build -o bin/fx-svc ./services/fx-svc/cmd/fx-svc/

# Run (infra must be up)
LOG_PRETTY=true ./bin/fx-svc

# Health check
curl http://localhost:9103/healthz

# Metrics
curl http://localhost:9103/metrics
```

### Test RPCs with grpcurl

```bash
# GetQuote — direct rate (USD → INR)
grpcurl -plaintext -d '{
  "from_currency": "USD",
  "to_currency": "INR",
  "source_amount": {"amount": "1000.0000", "currency": "USD"}
}' localhost:50053 fx.v1.FXService/GetQuote

# GetQuote — inverse rate resolution (INR → USD, derived from 1/USD→INR)
grpcurl -plaintext -d '{
  "from_currency": "INR",
  "to_currency": "USD",
  "source_amount": {"amount": "83500.0000", "currency": "INR"}
}' localhost:50053 fx.v1.FXService/GetQuote

# GetQuote — unknown pair (expect NOT_FOUND)
grpcurl -plaintext -d '{
  "from_currency": "INR",
  "to_currency": "SGD",
  "source_amount": {"amount": "100.0000", "currency": "INR"}
}' localhost:50053 fx.v1.FXService/GetQuote

# LockQuote — lock a rate (use quote_id from GetQuote response)
grpcurl -plaintext -d '{
  "quote_id": "<quote_id_from_above>",
  "idempotency_key": "lock-001"
}' localhost:50053 fx.v1.FXService/LockQuote

# LockQuote — idempotent replay (same key → locked: true again)
grpcurl -plaintext -d '{
  "quote_id": "<same_quote_id>",
  "idempotency_key": "lock-001"
}' localhost:50053 fx.v1.FXService/LockQuote

# LockQuote — double-lock prevention (different key → locked: false)
grpcurl -plaintext -d '{
  "quote_id": "<same_quote_id>",
  "idempotency_key": "lock-002"
}' localhost:50053 fx.v1.FXService/LockQuote

# Verify in Postgres directly
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, from_cur, to_cur, rate FROM fx.rates;"
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, from_currency, to_currency, rate, locked, expires_at FROM fx.quotes ORDER BY created_at DESC LIMIT 5;"
```

### FX via REST (BFF)

```bash
# Get a quote
curl -s -X POST http://localhost:8090/api/fx/quote \
  -H "Content-Type: application/json" \
  -d '{"from_currency":"USD","to_currency":"INR","amount":"500.00"}' | python3 -m json.tool

# Lock the returned quote (use quote.id from above)
curl -s -X POST http://localhost:8090/api/fx/quote/<quote_id>/lock \
  -H "Content-Type: application/json" \
  -d '{"idempotency_key":"ui-lock-001"}' | python3 -m json.tool
```

---

## Phase 8 — transaction-svc

```bash
# Build
go build -o bin/transaction-svc ./services/transaction-svc/cmd/transaction-svc/

# Run (ledger-svc + account-svc must be running first)
LOG_PRETTY=true ./bin/transaction-svc

# Health check
curl http://localhost:9104/healthz

# Metrics
curl http://localhost:9104/metrics
```

### Test RPCs with grpcurl

```bash
# Step 1: Create source + dest accounts and fund source
SRC=$(grpcurl -plaintext -d '{
  "idempotency_key": "owner-src-001", "owner_id": "user-alice", "currency": "INR"
}' localhost:50052 account.v1.AccountService/CreateAccount | python3 -c "import sys,json; print(json.load(sys.stdin)['account']['id'])")

DST=$(grpcurl -plaintext -d '{
  "idempotency_key": "owner-dst-001", "owner_id": "user-bob", "currency": "INR"
}' localhost:50052 account.v1.AccountService/CreateAccount | python3 -c "import sys,json; print(json.load(sys.stdin)['account']['id'])")

grpcurl -plaintext -d "{
  \"idempotency_key\": \"topup-alice-001\",
  \"account_id\": \"$SRC\",
  \"amount\": {\"amount\": \"10000.0000\", \"currency\": \"INR\"},
  \"reference_id\": \"topup-001\"
}" localhost:50052 account.v1.AccountService/CreditAccount

# Step 2: InitiatePayment — runs full saga
grpcurl -plaintext -d "{
  \"idempotency_key\": \"pay-001\",
  \"source_account_id\": \"$SRC\",
  \"dest_account_id\":   \"$DST\",
  \"amount\": {\"amount\": \"2500.0000\", \"currency\": \"INR\"},
  \"description\": \"invoice payment\"
}" localhost:50054 transaction.v1.TransactionService/InitiatePayment

# Step 3: GetTransaction (use id from above)
grpcurl -plaintext -d '{"transaction_id": "<id>"}' \
  localhost:50054 transaction.v1.TransactionService/GetTransaction

# Step 4: Idempotency replay — same key, same result, no double-charge
grpcurl -plaintext -d "{
  \"idempotency_key\": \"pay-001\",
  \"source_account_id\": \"$SRC\",
  \"dest_account_id\":   \"$DST\",
  \"amount\": {\"amount\": \"2500.0000\", \"currency\": \"INR\"}
}" localhost:50054 transaction.v1.TransactionService/InitiatePayment

# Step 5: Verify balances
grpcurl -plaintext -d "{\"account_id\": \"$SRC\"}" localhost:50052 account.v1.AccountService/GetAccount
grpcurl -plaintext -d "{\"account_id\": \"$DST\"}" localhost:50052 account.v1.AccountService/GetAccount

# Step 6: Inspect saga trace (HTTP — replace <id> with the transaction id from Step 2)
curl http://localhost:9104/trace/<id>

# Step 7: Inspect saga trace via API gateway (requires JWT)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/transactions/<id>/trace

# Step 6: Verify outbox event
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT aggregate_id, event_type FROM transaction.outbox_messages ORDER BY created_at DESC LIMIT 5;"

# Step 7: Verify ledger double-entry
grpcurl -plaintext -d "{\"account_id\": \"$SRC\", \"page_size\": 5}" \
  localhost:50051 ledger.v1.LedgerService/ListEntries

# Step 8: Insufficient funds — expect FAILED_PRECONDITION
grpcurl -plaintext -d "{
  \"idempotency_key\": \"pay-broke-001\",
  \"source_account_id\": \"$SRC\",
  \"dest_account_id\":   \"$DST\",
  \"amount\": {\"amount\": \"999999.0000\", \"currency\": \"INR\"}
}" localhost:50054 transaction.v1.TransactionService/InitiatePayment

# Step 9: REST via BFF
curl -s -X POST http://localhost:8090/api/transactions \
  -H "Content-Type: application/json" \
  -d "{\"idempotency_key\":\"pay-rest-001\",\"source_account_id\":\"$SRC\",\"dest_account_id\":\"$DST\",\"amount\":\"500.0000\",\"currency\":\"INR\"}" \
  | python3 -m json.tool
```

---

## Phase 9 — payout-svc + recon-worker

```bash
# Build
go build -o bin/payout-svc   ./services/payout-svc/cmd/payout-svc/
go build -o bin/recon-worker ./services/recon-worker/cmd/recon-worker/

# Run payout-svc (ledger-svc + account-svc must be running)
LOG_PRETTY=true ./bin/payout-svc

# Run recon-worker (30s interval for dev; default 60s)
LOG_PRETTY=true RECON_INTERVAL_SECONDS=30 ./bin/recon-worker

# Health checks
curl http://localhost:9105/healthz   # payout-svc
curl http://localhost:9106/healthz   # recon-worker
```

### Test payout-svc with grpcurl

```bash
# Fund an account first if needed
ACC="<account_id>"
grpcurl -plaintext -d "{
  \"idempotency_key\":\"fund-payout-test\",\"account_id\":\"$ACC\",
  \"amount\":{\"amount\":\"10000.0000\",\"currency\":\"INR\"},\"reference_id\":\"topup\"
}" localhost:50052 account.v1.AccountService/CreditAccount

# InitiatePayout — NACH rail (India bank transfer)
grpcurl -plaintext -d "{
  \"idempotency_key\": \"payout-001\",
  \"account_id\": \"$ACC\",
  \"amount\": {\"amount\": \"5000.0000\", \"currency\": \"INR\"},
  \"rail\": \"PAYOUT_RAIL_NACH\",
  \"destination\": {
    \"account_number\": \"123456789012\",
    \"ifsc_code\": \"HDFC0001234\",
    \"account_name\": \"Beneficiary Name\"
  }
}" localhost:50055 payout.v1.PayoutService/InitiatePayout

# InitiatePayout — SEPA rail (Europe)
grpcurl -plaintext -d "{
  \"idempotency_key\": \"payout-sepa-001\",
  \"account_id\": \"$ACC\",
  \"amount\": {\"amount\": \"200.0000\", \"currency\": \"EUR\"},
  \"rail\": \"PAYOUT_RAIL_SEPA\",
  \"destination\": {
    \"iban\": \"DE89370400440532013000\",
    \"bic\": \"COBADEFFXXX\",
    \"account_name\": \"Hans Mueller\"
  }
}" localhost:50055 payout.v1.PayoutService/InitiatePayout

# GetPayout
grpcurl -plaintext -d '{"payout_id":"<id>"}' \
  localhost:50055 payout.v1.PayoutService/GetPayout

# Idempotency replay (same key → same payout, no double debit)
grpcurl -plaintext -d "{
  \"idempotency_key\": \"payout-001\",
  \"account_id\": \"$ACC\",
  \"amount\": {\"amount\": \"5000.0000\", \"currency\": \"INR\"},
  \"rail\": \"PAYOUT_RAIL_NACH\",
  \"destination\": {\"account_number\": \"123456789012\", \"ifsc_code\": \"HDFC0001234\", \"account_name\": \"Beneficiary Name\"}
}" localhost:50055 payout.v1.PayoutService/InitiatePayout

# Verify in Postgres
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, status, rail, amount, batch_id, settlement_file FROM payout.payouts;"

# Verify outbox event
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT event_type, aggregate_id FROM payout.outbox_messages ORDER BY created_at DESC LIMIT 3;"
```

### Test recon-worker

```bash
# Recon runs on startup and then every RECON_INTERVAL_SECONDS
# Watch logs — it reports drift for each account where balance != ledger sum
# "covered"   = has ledger entries but balance differs (direct credits not tracked in ledger)
# "uncovered" = no ledger entries at all (fully funded via direct REST calls)

# Verify recon report uploaded to MinIO (recon-reports bucket)
# Reports are named: YYYY-MM-DDTHH-MM-SS.json
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT id, balance FROM account.accounts WHERE balance > 0 LIMIT 5;"
```

---

## Phase 10 — API Gateway

```bash
# Build
go build -o bin/gateway ./services/gateway/cmd/gateway/

# Set env (already in .env)
# JWT_SECRET, ADMIN_USERNAME, ADMIN_PASSWORD, UPSTREAM_URL, GATEWAY_PORT, GATEWAY_METRICS_PORT

# Run
JWT_SECRET=super-secret-ledger-jwt-key-change-in-prod \
ADMIN_USERNAME=admin \
ADMIN_PASSWORD=admin \
UPSTREAM_URL=http://localhost:8090 \
./bin/gateway

# Verify: login
curl -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
# → {"token":"eyJ...","expires_in":3600}

# Verify: unauthenticated request → 401
curl -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/health
# → 401

# Verify: authenticated request → 200
TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/health
# → {"healthy":true,"services":[...6 services...]}

# Verify: wrong password → 401
curl -s -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"wrong"}'
# → {"error":"invalid credentials"}

# Open the UI via gateway (login page appears)
# http://localhost:8080

# Healthz sidecar
curl http://localhost:9107/healthz
# → {"status":"ok","service":"gateway"}

# Prometheus metrics
curl http://localhost:9107/metrics | grep go_goroutines
```

---

## Phase 11 — Kafka Consumers

```bash
# Build
go build -o bin/consumers ./services/consumers/cmd/consumers/

# Run (requires Postgres + Redpanda running via docker compose)
DATABASE_URL="postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable" \
KAFKA_BROKERS=localhost:19092 \
KAFKA_GROUP_ID=ledger-consumers \
CONSUMERS_METRICS_PORT=9108 \
LOG_PRETTY=1 \
./bin/consumers

# Verify healthz
curl http://localhost:9108/healthz
# → {"status":"ok","service":"consumers"}

# Verify audit events (after any payment or payout)
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT event_type, aggregate_id, processed_at FROM audit.events ORDER BY processed_at DESC LIMIT 10;"

# Verify analytics counters
docker exec ledger-postgres psql -U ledger -d ledger \
  -c "SELECT event_type, period, count FROM analytics.event_counts ORDER BY count DESC;"

# Watch relay logs — publishes outbox rows every RELAY_INTERVAL_SECONDS (default 5s)
# "outbox relayed" log line shows how many events were published per schema
```

---

## Phase 12 — Tests

```bash
# Run full unit test suite across all modules (with race detector)
./scripts/test-all.sh

# Run a single module's tests
cd services/gateway  && go test -race -count=1 ./...
cd services/transaction-svc && go test -race -count=1 ./internal/service/...
cd services/payout-svc      && go test -race -count=1 ./internal/service/...

# Run internal shared library tests
cd internal && go test -race -count=1 ./...
```

**Test coverage per module:**

| Module | Test files | Cases |
|--------|-----------|-------|
| `internal/errors` | errors_test.go | error wrapping, codes |
| `internal/idempotency` | idempotency_test.go | Get/Set/NX semantics |
| `internal/logging` | logging_test.go | JSON + pretty output |
| `internal/metrics` | metrics_test.go | counter, histogram |
| `internal/money` | money_test.go | decimal arithmetic |
| `internal/outbox` | outbox_test.go | marshal/unmarshal |
| `internal/tracing` | tracing_test.go | span creation |
| `services/gateway/internal/auth` | jwt_test.go | Sign, Verify, expired, tampered, wrong secret |
| `services/gateway/internal/middleware` | auth_test.go, ratelimit_test.go, cors_test.go | JWT enforcement, non-API bypass, rate limiting, CORS preflight |
| `services/transaction-svc/internal/service` | transaction_test.go | 6 validation error cases |
| `services/payout-svc/internal/service` | payout_test.go | 6 validation cases incl. negative amount guard |

---

## Phase 13 — Helm + CI/CD

### Build Docker images locally

```bash
# Each image is built from the repo root with the service-specific Dockerfile.
# The go.work file is rewritten inside the build to drop unused modules.

docker build -f services/ledger-svc/Dockerfile      -t ledger-svc:local      .
docker build -f services/account-svc/Dockerfile     -t account-svc:local     .
docker build -f services/fx-svc/Dockerfile          -t fx-svc:local          .
docker build -f services/transaction-svc/Dockerfile -t transaction-svc:local .
docker build -f services/payout-svc/Dockerfile      -t payout-svc:local      .
docker build -f services/recon-worker/Dockerfile    -t recon-worker:local    .
docker build -f services/gateway/Dockerfile         -t gateway:local         .
docker build -f services/consumers/Dockerfile       -t consumers:local       .
docker build -f services/ui-bff/Dockerfile          -t ui-bff:local          .

# Final image sizes are ~25 MB (distroless/static-debian12 runtime)
docker images | grep ':local'
```

### Helm — render and inspect

```bash
# Dry-run template render (no cluster needed)
helm template ledger helm/ledger/ --set global.imageTag=local | head -60

# Lint the chart
helm lint helm/ledger/

# Install/upgrade to a local Kubernetes cluster (e.g. kind or k3d)
helm upgrade --install ledger helm/ledger/ \
  --namespace ledger \
  --create-namespace \
  --set global.imageTag=local \
  --set global.postgres.url="postgres://ledger:ledger@postgres:5432/ledger?sslmode=disable" \
  --set global.redis.url="redis://redis:6379" \
  --set services.gateway.env.JWT_SECRET="change-me" \
  --wait

# Verify rollout
kubectl get pods -n ledger
kubectl rollout status deployment/gateway -n ledger

# Uninstall
helm uninstall ledger -n ledger
```

### CI/CD — GitHub Actions

Workflows trigger automatically when code is pushed to GitHub:

| Workflow | Trigger | Jobs |
|----------|---------|------|
| `ci.yml` | every push + PR | lint (go vet) → test (race) → build binaries → helm lint |
| `cd.yml` | push to `main` | build & push 9 Docker images → helm upgrade (if `DEPLOY_ENABLED=true`) |

**Required GitHub secrets for CD deploy:**
- `KUBECONFIG` — base64 kubeconfig for target cluster
- `POSTGRES_URL`, `REDIS_URL` — connection strings
- `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY` — object store credentials
- `JWT_SECRET`, `ADMIN_PASSWORD` — gateway auth

**Enable deployment:**
```
# In GitHub → Settings → Variables → Actions
DEPLOY_ENABLED = true
```

---

## UI Overhaul (post Phase 13)

Complete replacement of the React SPA with a production-quality end-to-end UI.

### v2 — Dynamic & Animated UI (2026-05-22)

Full visual redesign of every page and component. Dark premium fintech aesthetic with micro-interactions.

| File | Change |
|------|--------|
| `ui/src/index.css` | Shimmer skeletons, glow utilities, gradient text, keyframe animations, new color palette `#0a0b0f` base |
| `ui/src/components/Sidebar.jsx` | Gradient active indicator with glow, animated sign-out icon, gradient logo |
| `ui/src/components/Toast.jsx` | Progress drain bar, slide-in/out animation, close button, info type added |
| `ui/src/components/Modal.jsx` | Scale-in animation, top gradient accent line, glow box-shadow |
| `ui/src/pages/Login.jsx` | Animated dot-grid background, icon inputs, feature cards with staggered fade-up |
| `ui/src/pages/Dashboard.jsx` | Stat row (services/healthy/latency), SVG countdown ring, latency bars per service row, staggered card animations |
| `ui/src/pages/FX.jsx` | Live SVG countdown ring on each quote card (green→amber→red), animated lock confirmation, rail color |
| `ui/src/pages/Accounts.jsx` | Per-currency color accent, balance utilization bar (available vs hold), currency picker grid in create modal |
| `ui/src/pages/Transactions.jsx` | Animated pulsing arrow for pending txns, route preview while filling form, status counts in header |
| `ui/src/pages/Payouts.jsx` | Rail visual selector (NACH/SEPA/SWIFT with colors), rail-colored submit button, RailBadge component |
| `ui/src/pages/Ledger.jsx` | AmountBar column (relative magnitude visualization), total credits/debits in balance strip, glow on balance |

### Key new features

- **FX countdown ring** — live SVG circle timer on every quote card; transitions green→amber→red
- **Dashboard stats row** — shows total services, healthy count, average latency, overall platform status
- **Latency bars** — mini horizontal bar in each service row; color-coded green/amber/red
- **Balance utilization bar** — on every account card, green=available, amber=on-hold
- **Rail visual selector** — click NACH/SEPA/SWIFT instead of a dropdown; colored per rail
- **Amount magnitude bars** — in Ledger entries table, shows relative size of each entry
- **Staggered animations** — all lists/cards animate in with `fade-up` stagger
- **fx-svc rate TTL fix** — seed TTL changed from 24h → 365d; restart required after 24h otherwise rates expire
- **Tutorial tab** — new `GraduationCap`-icon sidebar entry; 8-step interactive flow (intro + 7 real API calls)

### Tutorial page (`ui/src/pages/Tutorial.jsx`)

Interactive end-to-end tutorial with live API calls. Structure:

| Step | ID | What |
|------|----|------|
| 0 | `intro` | Platform overview — flow diagram, concept cards, actors. No API call; "Get Started" advances to step 1 |
| 1 | `create-alice` | POST `/api/accounts` — INR wallet for Alice (owner ID = `crypto.randomUUID()`) |
| 2 | `create-bob` | POST `/api/accounts` — INR wallet for Bob |
| 3 | `fund-alice` | POST `/api/accounts/{id}/credit` — ₹50,000 |
| 4 | `fx-quote` | POST `/api/fx/quote` + lock — USD→INR 30 s locked rate |
| 5 | `send-payment` | POST `/api/transactions` — ₹1,000 Alice→Bob; polls until COMPLETED |
| 6 | `check-ledger` | GET ledger entries + balances for both accounts |
| 7 | `payout` | POST `/api/payouts` — Alice withdraws ₹500 via NACH |

Key design notes:
- `genSeeds()` produces stable UUIDs per session via `crypto.randomUUID()`; `reset()` regenerates them
- `apiSteps = STEPS.filter(s => !s.intro)` — progress bar/counter excludes intro step
- `runAll` skips intro, iterates only API steps
- `prevsDone` logic gates each step behind its predecessors completing
- `JsonBlock` — regex syntax-highlighted JSON viewer (no external dependency)
