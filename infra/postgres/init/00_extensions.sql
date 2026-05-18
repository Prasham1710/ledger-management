-- Extensions loaded before any schema or table is created.
-- pgcrypto     : gen_random_uuid() for UUID v4 generation in SQL
-- uuid-ossp    : uuid_generate_v4() alternative (belt-and-suspenders)
-- pg_stat_statements : slow query tracking (enabled via shared_preload_libraries)

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
