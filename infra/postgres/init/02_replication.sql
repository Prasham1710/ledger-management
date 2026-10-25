-- Logical replication setup.
--
-- wal_level=logical is set via postgres command flags in docker-compose.
--
-- No publication is created here, and no replication slot is pre-created.
--
-- Why this file no longer creates anything
-- ----------------------------------------
-- It previously ran:
--
--     SELECT pg_create_logical_replication_slot('ledger_outbox_slot', 'pgoutput') ...
--     CREATE PUBLICATION ledger_outbox_pub FOR ALL TABLES;
--
-- Three problems:
--
-- 1. FOR ALL TABLES published every table in the database into the replication
--    stream, including account.accounts and payout.payouts — the latter holding
--    beneficiary bank account numbers and IBANs in its destination JSONB. A
--    connector's table.include.list filters what it *consumes*; the publication
--    still streams everything into the slot. Personal financial data should not
--    be in a replication stream nothing needs.
--
-- 2. This file's own comments claimed each service migration would run
--    "ALTER PUBLICATION ledger_outbox_pub ADD TABLE <schema>.outbox_messages".
--    No migration ever did. FOR ALL TABLES made it work by accident, which is
--    why the discrepancy went unnoticed.
--
-- 3. It conflicted with the Debezium connector's publication.autocreate.mode,
--    producing the "publication already active" failure that procedure.md
--    documented a manual workaround for.
--
-- The outbox connector has since been removed entirely: event publication is
-- owned by the Go relay in services/consumers, which polls the outbox tables
-- directly and needs no logical replication. See infra/debezium/README.md.
--
-- An unused replication slot is not harmless either: Postgres retains WAL
-- indefinitely for an inactive slot, so a slot nothing reads eventually fills
-- the disk.
--
-- If you add a CDC consumer
-- -------------------------
-- Create a publication naming only the tables it needs, never FOR ALL TABLES:
--
--     CREATE PUBLICATION my_feature_pub FOR TABLE some_schema.some_table;
--
-- and let the connector manage its own slot, so the slot's lifetime matches the
-- consumer's.

-- Confirm logical decoding is enabled, so a misconfigured server surfaces the
-- problem at init rather than when a connector is first registered.
DO $$
BEGIN
    IF current_setting('wal_level') <> 'logical' THEN
        RAISE WARNING 'wal_level is %, not logical — logical replication is unavailable',
            current_setting('wal_level');
    END IF;
END
$$;
