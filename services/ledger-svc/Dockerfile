FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/ledger-svc/ services/ledger-svc/
# Drop modules not present in this build context so go.work is valid.
RUN for svc in account-svc consumers fx-svc gateway payout-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/ledger-svc \
    ./services/ledger-svc/cmd/ledger-svc/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/ledger-svc /ledger-svc
EXPOSE 50051 9101
ENTRYPOINT ["/ledger-svc"]
