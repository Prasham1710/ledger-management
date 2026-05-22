FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/consumers/ services/consumers/
RUN for svc in account-svc fx-svc gateway ledger-svc payout-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/consumers \
    ./services/consumers/cmd/consumers/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/consumers /consumers
EXPOSE 9108
ENTRYPOINT ["/consumers"]
