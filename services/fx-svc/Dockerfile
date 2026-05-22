FROM golang:1.26-alpine AS builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/fx-svc/ services/fx-svc/
RUN for svc in account-svc consumers gateway ledger-svc payout-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/fx-svc \
    ./services/fx-svc/cmd/fx-svc/

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/fx-svc /fx-svc
EXPOSE 50053 9103
ENTRYPOINT ["/fx-svc"]
