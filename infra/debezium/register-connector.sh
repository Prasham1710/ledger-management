#!/bin/sh
# Registers the Debezium outbox connector via the Connect REST API.
# Postgres credentials are read from env vars injected by docker-compose.
# The static outbox-connector.json file does NOT contain any credentials.

set -eu

: "${POSTGRES_USER:?POSTGRES_USER env var is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD env var is required}"
: "${POSTGRES_DB:?POSTGRES_DB env var is required}"

DEBEZIUM_URL="http://debezium:8083"
CONNECTOR_NAME="ledger-outbox-connector"
MAX_WAIT=120
elapsed=0

echo "Waiting for Debezium Connect REST API..."
until curl -sf "${DEBEZIUM_URL}/connectors" > /dev/null; do
  if [ "$elapsed" -ge "$MAX_WAIT" ]; then
    echo "ERROR: Debezium did not become ready within ${MAX_WAIT}s"
    exit 1
  fi
  sleep 3
  elapsed=$((elapsed + 3))
done

echo "Building connector config from environment..."

# Credentials are injected here at runtime — never written to disk.
CONNECTOR_JSON=$(cat /scripts/outbox-connector.json \
  | sed "s/__POSTGRES_USER__/${POSTGRES_USER}/g" \
  | sed "s/__POSTGRES_PASSWORD__/${POSTGRES_PASSWORD}/g" \
  | sed "s/__POSTGRES_DB__/${POSTGRES_DB}/g")

echo "Registering outbox connector..."

HTTP_STATUS=$(
  curl -sf -o /dev/null -w "%{http_code}" \
    -X PUT \
    -H "Content-Type: application/json" \
    --data-binary "${CONNECTOR_JSON}" \
    "${DEBEZIUM_URL}/connectors/${CONNECTOR_NAME}/config" \
  || echo "000"
)

if [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "PUT returned ${HTTP_STATUS}, trying POST..."
  curl -sf \
    -X POST \
    -H "Content-Type: application/json" \
    --data-binary "${CONNECTOR_JSON}" \
    "${DEBEZIUM_URL}/connectors"
fi

echo ""
echo "Connector status:"
curl -sf "${DEBEZIUM_URL}/connectors/${CONNECTOR_NAME}/status" | cat
echo ""
echo "Done — outbox connector registered."
