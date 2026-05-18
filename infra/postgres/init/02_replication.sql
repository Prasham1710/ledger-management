-- Logical replication setup for Debezium CDC.
-- wal_level=logical is set via postgres command flags in docker-compose.
--
-- The publication is created here so Debezium can subscribe immediately
-- without needing superuser CREATE PUBLICATION rights at runtime.
-- Tables are added to the publication by service migrations (Part 5-9)
-- using: ALTER PUBLICATION ledger_outbox_pub ADD TABLE <schema>.outbox_messages;
--
-- publication.autocreate.mode=filtered in the Debezium connector config
-- means Debezium will manage the publication if it does not exist.
-- This file ensures it exists before any service runs.

SELECT pg_create_logical_replication_slot('ledger_outbox_slot', 'pgoutput')
WHERE NOT EXISTS (
    SELECT 1 FROM pg_replication_slots WHERE slot_name = 'ledger_outbox_slot'
);

-- The publication starts empty. Each service migration adds its outbox
-- table with: ALTER PUBLICATION ledger_outbox_pub ADD TABLE ...
CREATE PUBLICATION ledger_outbox_pub FOR ALL TABLES;
