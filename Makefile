.PHONY: help deps tidy test test-race lint build generate clean \
        infra-up infra-down infra-clean infra-logs infra-ps infra-wait

# Default target
help:
	@echo "Usage:"
	@echo ""
	@echo "  Infrastructure:"
	@echo "  make infra-up      Start all Docker Compose services (detached)"
	@echo "  make infra-down    Stop all services (keep volumes)"
	@echo "  make infra-clean   Stop all services and delete all volumes"
	@echo "  make infra-logs    Follow logs for all services"
	@echo "  make infra-ps      Show running container status"
	@echo "  make infra-wait    Block until all healthchecks pass"
	@echo ""
	@echo "  Go:"
	@echo "  make deps          Download all module dependencies"
	@echo "  make tidy          Run go mod tidy in every module"
	@echo "  make test          Run all tests (no race detector)"
	@echo "  make test-race     Run all tests with -race"
	@echo "  make lint          Run golangci-lint"
	@echo "  make build         Build all service binaries to ./bin/"
	@echo "  make generate      Run buf generate + oapi-codegen"
	@echo "  make clean         Remove build artefacts"

MODULES := \
	./gen \
	./internal \
	./services/ledger-svc \
	./services/account-svc \
	./services/fx-svc \
	./services/transaction-svc \
	./services/payout-svc \
	./services/recon-worker \
	./services/gateway \
	./services/consumers

# Download dependencies for every module in the workspace.
deps:
	go work sync
	@for mod in $(MODULES); do \
		echo "==> go mod download $$mod"; \
		(cd $$mod && go mod download); \
	done

# Run go mod tidy in every module.
tidy:
	@for mod in $(MODULES); do \
		echo "==> go mod tidy $$mod"; \
		(cd $$mod && go mod tidy); \
	done

# Run tests in all packages across the workspace.
test:
	go test ./...

# Run tests with race detector (use in CI).
test-race:
	go test -race -count=1 ./...

# Run golangci-lint.
lint:
	golangci-lint run ./...

# Build all service binaries.
SERVICES := ledger-svc account-svc fx-svc transaction-svc payout-svc recon-worker gateway

build: $(addprefix bin/, $(SERVICES))

bin/%:
	@mkdir -p bin
	go build -o $@ ./services/$*/cmd/...

# Lint proto files.
proto-lint:
	buf lint proto

# Regenerate Go code from proto files.
# Run this after any .proto change.
proto-gen:
	buf generate

# Run buf generate (Part 4) and oapi-codegen (Part 10).
generate: proto-gen
	oapi-codegen -config services/gateway/openapi/.oapi-codegen.yaml services/gateway/openapi/api.yaml

clean:
	rm -rf bin/ gen/ coverage.txt coverage.html

# ─── Infrastructure targets ────────────────────────────────────────────────

# Start all infra containers in the background.
infra-up:
	docker compose up -d --remove-orphans
	@echo "Infra starting — run 'make infra-ps' to watch health."

# Stop containers but keep data volumes.
infra-down:
	docker compose down

# Stop containers AND delete all volumes (full reset).
infra-clean:
	docker compose down -v --remove-orphans
	@echo "All volumes deleted. Next 'make infra-up' starts fresh."

# Follow logs (Ctrl-C to stop).
infra-logs:
	docker compose logs -f

# Show container status and health.
infra-ps:
	docker compose ps

# Block until every service with a healthcheck reports healthy.
infra-wait:
	@echo "Waiting for all services to be healthy..."
	@until [ "$$(docker compose ps --format json | python3 -c \
	  "import sys,json; data=[json.loads(l) for l in sys.stdin if l.strip()]; \
	   unhealthy=[s['Name'] for s in data if s.get('Health','healthy') not in ('healthy','')]; \
	   print('\n'.join(unhealthy))" 2>/dev/null)" = "" ]; do \
	  sleep 3; \
	  docker compose ps --format "table {{.Name}}\t{{.Status}}" 2>/dev/null | grep -v "exited 0" || true; \
	done
	@echo "All services healthy."
