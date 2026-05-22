FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/recon-worker/ services/recon-worker/
RUN for svc in account-svc consumers fx-svc gateway ledger-svc payout-svc transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/recon-worker \
    ./services/recon-worker/cmd/recon-worker/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/recon-worker /recon-worker
EXPOSE 9106
ENTRYPOINT ["/recon-worker"]
