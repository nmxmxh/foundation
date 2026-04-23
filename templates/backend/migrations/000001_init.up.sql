-- Initial migration: Create base tables and extensions
-- Created: {{TIMESTAMP}}

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

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

-- ============================================================================
-- RIVER JOB QUEUE SCHEMA
-- River is a background job processing system for Go applications
-- ============================================================================

-- River job state bitmask function
-- Used for unique job constraints
CREATE OR REPLACE FUNCTION river_job_state_in_bitmask(bitmask BIT(8), state varchar)
RETURNS boolean
LANGUAGE SQL
IMMUTABLE
AS $$
    SELECT CASE state
        WHEN 'available' THEN get_bit(bitmask, 7)
        WHEN 'cancelled' THEN get_bit(bitmask, 6)
        WHEN 'completed' THEN get_bit(bitmask, 5)
        WHEN 'discarded' THEN get_bit(bitmask, 4)
        WHEN 'pending'   THEN get_bit(bitmask, 3)
        WHEN 'retryable' THEN get_bit(bitmask, 2)
        WHEN 'running'   THEN get_bit(bitmask, 1)
        WHEN 'scheduled' THEN get_bit(bitmask, 0)
        ELSE 0
    END = 1;
$$;

-- River job table
CREATE TABLE IF NOT EXISTS river_job (
    id bigserial PRIMARY KEY,
    state varchar(8) NOT NULL DEFAULT 'available',
    attempt smallint NOT NULL DEFAULT 0,
    max_attempts smallint NOT NULL,
    attempted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    finalized_at timestamptz,
    scheduled_at timestamptz NOT NULL DEFAULT NOW(),
    priority smallint NOT NULL DEFAULT 1,
    args jsonb,
    attempt_errors jsonb[],
    metadata jsonb,
    kind varchar(255) NOT NULL,
    queue varchar(255) NOT NULL DEFAULT 'default',
    tags varchar(255)[],
    unique_key bytea,
    unique_states bit(8)
);

-- River job indexes
CREATE INDEX river_job_args_index ON river_job USING GIN (args);
CREATE INDEX river_job_kind ON river_job (kind);
CREATE INDEX river_job_metadata_index ON river_job USING GIN (metadata);
CREATE INDEX river_job_prioritized_fetching_index ON river_job (state, priority, scheduled_at, id);
CREATE INDEX river_job_state_and_finalized_at_index ON river_job (state, finalized_at) WHERE finalized_at IS NOT NULL;

-- Unique index for job uniqueness
CREATE UNIQUE INDEX river_job_unique_idx ON river_job (unique_key)
    WHERE unique_key IS NOT NULL
      AND unique_states IS NOT NULL
      AND river_job_state_in_bitmask(unique_states, state);

-- River leader table (for cluster leader election)
CREATE TABLE IF NOT EXISTS river_leader (
    elected_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    leader_id text NOT NULL,
    name text PRIMARY KEY
);

-- River queue table
CREATE TABLE IF NOT EXISTS river_queue (
    name text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    metadata jsonb,
    paused_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

-- River client table
CREATE TABLE IF NOT EXISTS river_client (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    metadata jsonb,
    paused_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

-- River client queue table
CREATE TABLE IF NOT EXISTS river_client_queue (
    client_id text NOT NULL REFERENCES river_client(id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    max_workers bigint NOT NULL DEFAULT 0,
    metadata jsonb,
    num_jobs_completed bigint NOT NULL DEFAULT 0,
    num_jobs_running bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (client_id, name)
);
