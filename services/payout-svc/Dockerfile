FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/payout-svc/ services/payout-svc/
RUN for svc in account-svc consumers fx-svc gateway ledger-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/payout-svc \
    ./services/payout-svc/cmd/payout-svc/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/payout-svc /payout-svc
EXPOSE 50055 9105
ENTRYPOINT ["/payout-svc"]
