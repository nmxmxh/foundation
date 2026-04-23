-- canonical River bootstrap owned by foundation/server-kit
drop table if exists river_job_metadata cascade;
drop table if exists river_client_queue cascade;
drop table if exists river_client cascade;
drop table if exists river_leader cascade;
drop table if exists river_queue cascade;
drop table if exists river_job cascade;
drop function if exists river_job_state_in_bitmask(bitmask bit(8), state text);
drop function if exists river_job_state_in_bitmask(bitmask bit(8), state river_job_state);
drop type if exists river_job_state cascade;

create type river_job_state as enum (
  'available',
  'scheduled',
  'running',
  'retryable',
  'completed',
  'discarded',
  'cancelled',
  'pending'
);

create table if not exists river_job (
  id bigserial primary key,
  state river_job_state not null default 'available',
  queue varchar(255) not null default 'default',
  kind varchar(255) not null,
  args jsonb not null default '{}',
  attempt integer not null default 0,
  max_attempts integer not null default 25,
  errors jsonb[],
  attempted_at timestamptz,
  finalized_at timestamptz,
  scheduled_at timestamptz not null default now(),
  created_at timestamptz not null default now(),
  priority smallint not null default 1,
  tags varchar(255)[],
  metadata jsonb not null default '{}',
  unique_key bytea,
  unique_states bit(8),
  attempted_by text[]
);

create table if not exists river_queue (
  name text primary key,
  created_at timestamptz not null default now(),
  metadata jsonb not null default '{}',
  paused_at timestamptz,
  updated_at timestamptz not null default now()
);

create unlogged table if not exists river_leader (
  name text primary key default 'default',
  leader_id text not null,
  expires_at timestamptz not null,
  elected_at timestamptz not null,
  kind text not null default 'default'
);

create table if not exists river_client (
  id text primary key,
  created_at timestamptz not null default now(),
  metadata jsonb not null default '{}'::jsonb,
  paused_at timestamptz,
  updated_at timestamptz not null default now()
);

create table if not exists river_client_queue (
  client_id text not null references river_client(id) on delete cascade,
  name text not null,
  created_at timestamptz not null default now(),
  max_workers bigint not null default 0,
  metadata jsonb not null default '{}'::jsonb,
  num_jobs_completed bigint not null default 0,
  num_jobs_running bigint not null default 0,
  updated_at timestamptz not null default now(),
  primary key (client_id, name)
);

create or replace function river_job_state_in_bitmask(bitmask bit(8), state river_job_state)
returns boolean language sql immutable as $$
select case
  state
  when 'available' then get_bit(bitmask, 7) = 1
  when 'cancelled' then get_bit(bitmask, 6) = 1
  when 'completed' then get_bit(bitmask, 5) = 1
  when 'discarded' then get_bit(bitmask, 4) = 1
  when 'pending' then get_bit(bitmask, 3) = 1
  when 'retryable' then get_bit(bitmask, 2) = 1
  when 'running' then get_bit(bitmask, 1) = 1
  when 'scheduled' then get_bit(bitmask, 0) = 1
  else false
end
$$;

create index if not exists river_job_args_index on river_job using gin (args);
create index if not exists river_job_kind on river_job (kind);
create index if not exists river_job_metadata_index on river_job using gin (metadata);
create index if not exists river_job_prioritized_fetching_index on river_job (state, priority, scheduled_at, id);
create index if not exists river_job_state_and_finalized_at_index on river_job (state, finalized_at)
where finalized_at is not null;
create unique index if not exists river_job_unique_idx on river_job (unique_key)
where unique_key is not null
  and river_job_state_in_bitmask(unique_states, state);

create table if not exists river_job_metadata (
  job_id bigint primary key references river_job(id),
  workflow_name text not null,
  entity_type text not null,
  entity_id text not null,
  user_id text,
  correlation_id text,
  tracking_data jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
