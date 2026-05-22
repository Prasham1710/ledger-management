FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/transaction-svc/ services/transaction-svc/
RUN for svc in account-svc consumers fx-svc gateway ledger-svc payout-svc recon-worker ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/transaction-svc \
    ./services/transaction-svc/cmd/transaction-svc/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/transaction-svc /transaction-svc
EXPOSE 50054 9104
ENTRYPOINT ["/transaction-svc"]
