-- Append-only double-entry table.
-- Balances are never stored — they are projected by SUM(credits) - SUM(debits).
CREATE TABLE IF NOT EXISTS ledger.entries (
    id           UUID          NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    account_id   TEXT          NOT NULL,
    type         TEXT          NOT NULL,
    amount       NUMERIC(20,4) NOT NULL,
    currency     TEXT          NOT NULL,
    reference_id TEXT          NOT NULL,
    description  TEXT          NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_entries_type   CHECK (type IN ('DEBIT', 'CREDIT')),
    CONSTRAINT chk_entries_amount CHECK (amount > 0)
);

CREATE INDEX IF NOT EXISTS idx_entries_account_created
    ON ledger.entries (account_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_entries_reference
    ON ledger.entries (reference_id);

-- Outbox for CDC → Kafka (Debezium tails this table via WAL).
CREATE TABLE IF NOT EXISTS ledger.outbox_messages (
    id             UUID        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
