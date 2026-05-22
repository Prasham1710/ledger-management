# Stage 1: Build React SPA
FROM node:18-alpine AS ui-builder
WORKDIR /app
COPY ui/package*.json ./
RUN npm ci --prefer-offline
COPY ui/ .
RUN npm run build

# Stage 2: Build gateway binary
FROM golang:1.26-alpine AS go-builder
WORKDIR /workspace
COPY go.work go.work.sum ./
COPY gen/      gen/
COPY internal/ internal/
COPY services/gateway/ services/gateway/
RUN for svc in account-svc consumers fx-svc ledger-svc payout-svc recon-worker transaction-svc ui-bff; do \
      go work edit -dropuse="./services/$svc" 2>/dev/null || true; \
    done
RUN go build -ldflags="-s -w" -trimpath \
    -o /bin/gateway \
    ./services/gateway/cmd/gateway/

# Stage 3: Minimal runtime
FROM gcr.io/distroless/static-debian12
COPY --from=go-builder /bin/gateway /gateway
COPY --from=ui-builder /app/dist     /ui/dist
ENV STATIC_DIR=/ui/dist
EXPOSE 8080 9107
ENTRYPOINT ["/gateway"]
