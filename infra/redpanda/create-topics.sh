#!/usr/bin/env bash
# Creates all Kafka topics on Redpanda at first startup.
# Runs as a one-shot container after redpanda is healthy.
# Using || true on each create so re-runs are idempotent.

set -euo pipefail

BROKER="redpanda:9092"

create_topic() {
  local name="$1"
  local partitions="${2:-10}"
  local retention_ms="${3:-604800000}"   # 7 days default

  echo "  → $name (partitions=$partitions, retention=${retention_ms}ms)"
  rpk topic create "$name" \
    --brokers "$BROKER" \
    --partitions "$partitions" \
    --replicas 1 \
    --topic-config "retention.ms=${retention_ms}" \
    --topic-config "cleanup.policy=delete" \
    2>&1 || true   # idempotent: ignore "already exists"
}

echo "=== Creating payment event topics ==="
# Partitioned by account_id (done by producers) — 10 partitions gives
# enough parallelism without wasting resources in single-node local dev.
create_topic "payments.payment.completed"  10
create_topic "payments.payment.failed"     10
create_topic "payments.payout.initiated"   10
create_topic "payments.payout.finalized"   10
create_topic "payments.hold.matured"       10
create_topic "payments.recon.drift"        3   # low volume, ops alerts

echo "=== Creating dead-letter queue topics ==="
# DLQ messages are retained for 30 days so ops can replay them.
create_topic "payments.dlq.audit"        3   2592000000
create_topic "payments.dlq.notification" 3   2592000000
create_topic "payments.dlq.analytics"    3   2592000000

echo "=== Creating Debezium internal topics ==="
# Kafka Connect REQUIRES cleanup.policy=compact on its internal topics.
# Using delete causes the Connect worker to abort on startup.
echo "  → debezium.configs (partitions=1, compact)"
rpk topic create "debezium.configs" --brokers "$BROKER" --partitions 1 --replicas 1 \
  --topic-config "cleanup.policy=compact" 2>&1 || true

echo "  → debezium.offsets (partitions=25, compact)"
rpk topic create "debezium.offsets" --brokers "$BROKER" --partitions 25 --replicas 1 \
  --topic-config "cleanup.policy=compact" 2>&1 || true

echo "  → debezium.status (partitions=5, compact)"
rpk topic create "debezium.status" --brokers "$BROKER" --partitions 5 --replicas 1 \
  --topic-config "cleanup.policy=compact" 2>&1 || true

echo "=== All topics created ==="
rpk topic list --brokers "$BROKER"
