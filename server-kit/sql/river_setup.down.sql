-- canonical River teardown owned by foundation/server-kit
drop table if exists river_job_metadata;
drop table if exists river_client_queue;
drop table if exists river_client;
drop table if exists river_leader;
drop table if exists river_queue;
drop table if exists river_job;
drop function if exists river_job_state_in_bitmask(bitmask bit(8), state text);
drop function if exists river_job_state_in_bitmask(bitmask bit(8), state river_job_state);
drop type if exists river_job_state;
