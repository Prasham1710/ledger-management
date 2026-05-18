-- Schema-per-service isolation.
-- Each service owns exactly one schema — no cross-schema JOINs in application code.
-- The recon-worker is the single intentional exception: it reads both
-- ledger and account schemas to verify the zero-sum invariant.
--
-- Schemas are created here (empty). Tables are created by each service's
-- golang-migrate migrations at first startup.

CREATE SCHEMA IF NOT EXISTS ledger;      -- ledger-svc: append-only double-entry entries
CREATE SCHEMA IF NOT EXISTS account;     -- account-svc: wallets, holds, reserves
CREATE SCHEMA IF NOT EXISTS fx;          -- fx-svc: rate cache, quote locks
CREATE SCHEMA IF NOT EXISTS transaction; -- transaction-svc: sagas, outbox
CREATE SCHEMA IF NOT EXISTS payout;      -- payout-svc: withdrawal batches, bank files
CREATE SCHEMA IF NOT EXISTS consumer;    -- consumers: audit log, notification state

-- Grant full access on all schemas to the ledger application user.
-- In production, each service would have its own DB user with access to
-- its schema only. For local dev a single shared user is acceptable.
GRANT ALL PRIVILEGES ON SCHEMA ledger      TO ledger;
GRANT ALL PRIVILEGES ON SCHEMA account     TO ledger;
GRANT ALL PRIVILEGES ON SCHEMA fx          TO ledger;
GRANT ALL PRIVILEGES ON SCHEMA transaction TO ledger;
GRANT ALL PRIVILEGES ON SCHEMA payout      TO ledger;
GRANT ALL PRIVILEGES ON SCHEMA consumer    TO ledger;

-- Allow future tables created in each schema to be accessible automatically.
ALTER DEFAULT PRIVILEGES IN SCHEMA ledger      GRANT ALL ON TABLES TO ledger;
ALTER DEFAULT PRIVILEGES IN SCHEMA account     GRANT ALL ON TABLES TO ledger;
ALTER DEFAULT PRIVILEGES IN SCHEMA fx          GRANT ALL ON TABLES TO ledger;
ALTER DEFAULT PRIVILEGES IN SCHEMA transaction GRANT ALL ON TABLES TO ledger;
ALTER DEFAULT PRIVILEGES IN SCHEMA payout      GRANT ALL ON TABLES TO ledger;
ALTER DEFAULT PRIVILEGES IN SCHEMA consumer    GRANT ALL ON TABLES TO ledger;

-- Set default search_path so unqualified table names resolve to the right schema.
-- Each service's DSN should append ?search_path=<schema> to be explicit.
ALTER ROLE ledger SET search_path TO transaction, ledger, account, fx, payout, consumer, public;
