-- Initial migration: Create base tables and extensions
-- Created: {{TIMESTAMP}}

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_stat_statements";

-- Create updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Users table (example)
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    status VARCHAR(50) DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Create indexes
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_status ON users(status);

-- Foundation runtime state store
-- Mirrors server-kit/go/database.PostgresDB and MemoryDB StateStore semantics.
CREATE TABLE IF NOT EXISTS governance_state_records (
    id BIGSERIAL PRIMARY KEY,
    domain TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    organization_id TEXT NOT NULL DEFAULT '',
    record_id TEXT NOT NULL,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT governance_state_records_identity_unique
        UNIQUE (domain, collection_name, organization_id, record_id),
    CONSTRAINT governance_state_records_data_object
        CHECK (jsonb_typeof(data) = 'object')
);

CREATE TRIGGER update_governance_state_records_updated_at
    BEFORE UPDATE ON governance_state_records
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX IF NOT EXISTS idx_governance_state_scope_updated
    ON governance_state_records (domain, collection_name, organization_id, updated_at DESC, record_id ASC);
CREATE INDEX IF NOT EXISTS idx_governance_state_org
    ON governance_state_records (organization_id);
CREATE INDEX IF NOT EXISTS idx_governance_state_data_gin
    ON governance_state_records USING GIN (data jsonb_path_ops);
CREATE INDEX IF NOT EXISTS idx_governance_state_scope_state_updated
    ON governance_state_records (
        domain,
        collection_name,
        organization_id,
        btrim(data ->> 'state'),
        updated_at DESC,
        record_id ASC
    )
    WHERE data ? 'state';

-- Foundation durable event log
-- Stores typed binary EventEnvelope facts for outbox replay, Redis Stream
-- publication, Hermes rebuild/repair, audit, and analytics. Operational log
-- text must never be written here.
CREATE TABLE IF NOT EXISTS foundation_event_log (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL DEFAULT ('evt_' || gen_random_uuid()::text),
    event_type TEXT NOT NULL,
    organization_id TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    payload_encoding TEXT NOT NULL,
    envelope BYTEA NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL,
    source_node_id TEXT NOT NULL DEFAULT '',
    publish_stream TEXT,
    publish_stream_id TEXT,
    published_at TIMESTAMPTZ,
    publish_attempts INTEGER NOT NULL DEFAULT 0,
    last_publish_error TEXT,
    publish_claim_token TEXT,
    publish_claimed_at TIMESTAMPTZ,
    publish_claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT foundation_event_log_event_unique UNIQUE (event_id),
    CONSTRAINT foundation_event_log_metadata_object
        CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE TRIGGER update_foundation_event_log_updated_at
    BEFORE UPDATE ON foundation_event_log
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE foundation_event_log
    IS 'Durable typed event facts. Operational logs must not feed Hermes.';

CREATE INDEX IF NOT EXISTS idx_foundation_event_log_pending
    ON foundation_event_log (id)
    WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_foundation_event_log_claim
    ON foundation_event_log (publish_claim_expires_at, id)
    WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_foundation_event_log_org_time
    ON foundation_event_log (organization_id, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_foundation_event_log_type_time
    ON foundation_event_log (event_type, occurred_at DESC, id DESC);

-- ============================================================================
-- RIVER JOB QUEUE SCHEMA
-- River is a background job processing system for Go applications
-- ============================================================================

CREATE TYPE river_job_state AS ENUM (
  'available',
  'scheduled',
  'running',
  'retryable',
  'completed',
  'discarded',
  'cancelled',
  'pending'
);

-- River job table
CREATE TABLE IF NOT EXISTS river_job (
    id bigserial PRIMARY KEY,
    state river_job_state NOT NULL DEFAULT 'available',
    queue varchar(255) NOT NULL DEFAULT 'default',
    kind varchar(255) NOT NULL,
    args jsonb NOT NULL DEFAULT '{}',
    attempt integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 25,
    errors jsonb[],
    attempted_at timestamptz,
    finalized_at timestamptz,
    scheduled_at timestamptz NOT NULL DEFAULT NOW(),
    created_at timestamptz NOT NULL DEFAULT NOW(),
    priority smallint NOT NULL DEFAULT 1,
    tags varchar(255)[],
    metadata jsonb NOT NULL DEFAULT '{}',
    unique_key bytea,
    unique_states bit(8),
    attempted_by text[]
);

CREATE OR REPLACE FUNCTION river_job_state_in_bitmask(bitmask BIT(8), state river_job_state)
RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$
SELECT CASE
  state
  WHEN 'available' THEN get_bit(bitmask, 7) = 1
  WHEN 'cancelled' THEN get_bit(bitmask, 6) = 1
  WHEN 'completed' THEN get_bit(bitmask, 5) = 1
  WHEN 'discarded' THEN get_bit(bitmask, 4) = 1
  WHEN 'pending' THEN get_bit(bitmask, 3) = 1
  WHEN 'retryable' THEN get_bit(bitmask, 2) = 1
  WHEN 'running' THEN get_bit(bitmask, 1) = 1
  WHEN 'scheduled' THEN get_bit(bitmask, 0) = 1
  ELSE false
END
$$;

-- River job indexes
CREATE INDEX river_job_args_index ON river_job USING GIN (args);
CREATE INDEX river_job_kind ON river_job (kind);
CREATE INDEX river_job_metadata_index ON river_job USING GIN (metadata);
CREATE INDEX river_job_prioritized_fetching_index ON river_job (state, priority, scheduled_at, id);
CREATE INDEX river_job_state_and_finalized_at_index ON river_job (state, finalized_at) WHERE finalized_at IS NOT NULL;

CREATE UNIQUE INDEX river_job_unique_idx ON river_job (unique_key)
    WHERE unique_key IS NOT NULL
      AND river_job_state_in_bitmask(unique_states, state);

-- River leader table (for cluster leader election)
CREATE TABLE IF NOT EXISTS river_leader (
    name text PRIMARY KEY DEFAULT 'default',
    leader_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    elected_at timestamptz NOT NULL,
    kind text NOT NULL DEFAULT 'default'
);

-- River queue table
CREATE TABLE IF NOT EXISTS river_queue (
    name text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    metadata jsonb NOT NULL DEFAULT '{}',
    paused_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

-- River client table
CREATE TABLE IF NOT EXISTS river_client (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    paused_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

-- River client queue table
CREATE TABLE IF NOT EXISTS river_client_queue (
    client_id text NOT NULL REFERENCES river_client(id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    max_workers bigint NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    num_jobs_completed bigint NOT NULL DEFAULT 0,
    num_jobs_running bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (client_id, name)
);

-- River sidecar metadata owned by server-kit worker.Engine.
-- Cascades with River job pruning so lifecycle cleanup cannot orphan metadata.
CREATE TABLE IF NOT EXISTS river_job_metadata (
    job_id bigint PRIMARY KEY REFERENCES river_job(id) ON DELETE CASCADE,
    workflow_name text NOT NULL,
    entity_type text NOT NULL,
    entity_id text NOT NULL,
    user_id text,
    correlation_id text,
    raw_payload bytea,
    tracking_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);
