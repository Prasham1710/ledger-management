FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/account-svc/ services/account-svc/
RUN for svc in consumers fx-svc gateway ledger-svc payout-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/account-svc \
    ./services/account-svc/cmd/account-svc/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/account-svc /account-svc
EXPOSE 50052 9102
ENTRYPOINT ["/account-svc"]
