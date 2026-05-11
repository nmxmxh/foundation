-- Rollback initial migration

-- Drop River tables (in reverse dependency order)
DROP TABLE IF EXISTS river_job_metadata CASCADE;
DROP TABLE IF EXISTS river_client_queue CASCADE;
DROP TABLE IF EXISTS river_client CASCADE;
DROP TABLE IF EXISTS river_queue CASCADE;
DROP TABLE IF EXISTS river_leader CASCADE;
DROP TABLE IF EXISTS river_job CASCADE;
DROP FUNCTION IF EXISTS river_job_state_in_bitmask CASCADE;
DROP TYPE IF EXISTS river_job_state CASCADE;

-- Drop application tables
DROP TABLE IF EXISTS governance_state_records CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP FUNCTION IF EXISTS update_updated_at_column CASCADE;
