# Debezium

Debezium runs here as available CDC infrastructure. **It must not subscribe to
any `outbox_messages` table.**

## Why the outbox connector was removed

This directory previously registered `ledger-outbox-connector`, a Debezium
Postgres connector with an `EventRouter` transform over the four
`*.outbox_messages` tables, routing to `payments.${event_type}`.

The Go outbox relay in `services/consumers/internal/relay` publishes to exactly
those same topics. The result was two independent publishers for every event:

- **Every event was delivered twice.** `payment.completed` reached
  `payments.payment.completed` once from the relay and once from the WAL.
- **The two envelopes had different shapes.** The relay publishes
  `{id, aggregate_id, aggregate_type, event_type, payload}`; Debezium's
  `EventRouter` publishes the unwrapped payload. The consumer unmarshals the
  relay's shape, and a Debezium message parsed into it *without error*, leaving
  every field blank — so half the audit rows had an empty `event_type` and a null
  payload, with no log line to explain them.
- **The relay's row deletion raced the WAL reader**, making Debezium's view of
  the outbox depend on polling timing.
- Registration itself was already known-broken: `procedure.md` documented a
  manual workaround for the connector failing with "publication already active",
  because `publication.autocreate.mode` conflicted with the publication created
  in `infra/postgres/init/02_replication.sql`.

Event publication now has exactly one owner: the relay. It publishes, waits for
the broker acknowledgement, and only then marks the row published — at-least-once
delivery, deduplicated by consumers on the envelope `id`.

## If you add a connector

Use `table.include.list` to name the specific tables you need and confirm the
topics do not collide with `internal/outbox`'s topic registry. Do not add
`outbox_messages` tables.
