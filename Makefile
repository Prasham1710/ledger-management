.PHONY: help deps tidy test test-race test-cover lint lint-fix fmt vet build generate \
        proto-lint proto-gen clean verify \
        infra-up infra-down infra-clean infra-logs infra-ps infra-wait

# All Go modules in the workspace. ui-bff was previously missing from this list,
# so `make tidy`, `make deps`, and `make build` all silently skipped it.
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
	./services/consumers \
	./services/ui-bff

# Buildable service binaries. ui-bff was previously missing here too.
SERVICES := \
	ledger-svc \
	account-svc \
	fx-svc \
	transaction-svc \
	payout-svc \
	recon-worker \
	gateway \
	consumers \
	ui-bff

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
	@echo "  make fmt           Format all Go code"
	@echo "  make vet           Run go vet in every module"
	@echo "  make test          Run all tests"
	@echo "  make test-race     Run all tests with -race"
	@echo "  make test-cover    Run all tests and report coverage"
	@echo "  make lint          Run golangci-lint"
	@echo "  make build         Build all service binaries to ./bin/"
	@echo "  make generate      Regenerate protobuf code"
	@echo "  make verify        fmt check + vet + lint + test-race (what CI runs)"
	@echo "  make clean         Remove build artefacts"

# Download dependencies for every module in the workspace.
deps:
	go work sync
	@for mod in $(MODULES); do \
		echo "==> go mod download $$mod"; \
		(cd $$mod && go mod download) || exit 1; \
	done

# Run go mod tidy in every module.
tidy:
	@for mod in $(MODULES); do \
		echo "==> go mod tidy $$mod"; \
		(cd $$mod && go mod tidy) || exit 1; \
	done

fmt:
	gofmt -l -w $$(git ls-files '*.go' | grep -v '^gen/')

# fmt-check fails when anything is unformatted, for CI.
fmt-check:
	@unformatted=$$(gofmt -l $$(git ls-files '*.go' | grep -v '^gen/')); \
	if [ -n "$$unformatted" ]; then \
	  echo "These files are not gofmt-formatted:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "All files are formatted."

# go vet every module.
#
# Iterating over modules is required: the repository root is not itself a module,
# so `go vet ./...` from here fails with "directory prefix . does not contain
# modules listed in go.work". The old `test` and `lint` targets did exactly that
# and could never have run.
vet:
	@for mod in $(MODULES); do \
		echo "==> go vet $$mod"; \
		(cd $$mod && go vet ./...) || exit 1; \
	done

# Run tests in every module.
test:
	@failed=""; \
	for mod in $(MODULES); do \
		echo "━━━ $$mod ━━━"; \
		(cd $$mod && go test ./...) || failed="$$failed $$mod"; \
	done; \
	if [ -n "$$failed" ]; then echo "FAILED:$$failed"; exit 1; fi; \
	echo "All tests passed."

# Run tests with the race detector. Use this before committing.
test-race:
	./scripts/test-all.sh

test-cover:
	@for mod in $(MODULES); do \
		echo "━━━ $$mod ━━━"; \
		(cd $$mod && go test -cover ./...) || exit 1; \
	done

# Run golangci-lint in every module.
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint is not installed."; \
	  echo "Install it with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
	  exit 1; \
	}
	@for mod in $(MODULES); do \
		echo "==> golangci-lint $$mod"; \
		(cd $$mod && golangci-lint run ./...) || exit 1; \
	done

lint-fix:
	@for mod in $(MODULES); do \
		(cd $$mod && golangci-lint run --fix ./...) || exit 1; \
	done

# Build all service binaries.
build: $(addprefix bin/, $(SERVICES))

bin/%:
	@mkdir -p bin
	go build -ldflags="-s -w" -trimpath -o $@ ./services/$*/cmd/$*/

# Lint proto files.
proto-lint:
	buf lint proto

# Regenerate Go code from proto files. Run this after any .proto change.
proto-gen:
	buf generate

generate: proto-gen

# What CI runs, so a green local run means a green pipeline.
verify: fmt-check vet lint test-race

# clean removes build artefacts.
#
# It no longer deletes gen/. That directory holds generated protobuf code which
# is committed to the repository, and removing it left the workspace unbuildable
# for anyone without buf installed.
clean:
	rm -rf bin/ coverage.txt coverage.html

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
	@echo "Waiting until all services are healthy..."
	@until [ "$$(docker compose ps --format json | python3 -c \
	  "import sys,json; data=[json.loads(l) for l in sys.stdin if l.strip()]; \
	   unhealthy=[s['Name'] for s in data if s.get('Health','healthy') not in ('healthy','')]; \
	   print('\n'.join(unhealthy))" 2>/dev/null)" = "" ]; do \
	  sleep 3; \
	  docker compose ps --format "table {{.Name}}\t{{.Status}}" 2>/dev/null | grep -v "exited 0" || true; \
	done
	@echo "All services healthy."
