CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE credentials (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  op_service_account_token    BYTEA NOT NULL,
  vaultwarden_url             TEXT NOT NULL,
  vaultwarden_client_id       BYTEA NOT NULL,
  vaultwarden_client_secret   BYTEA NOT NULL,
  vaultwarden_master_password BYTEA NOT NULL,
  created_at                  TIMESTAMPTZ DEFAULT now(),
  updated_at                  TIMESTAMPTZ DEFAULT now(),
  UNIQUE (user_id)
);

CREATE TABLE sync_jobs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',
  started_at  TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error       TEXT,
  created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sync_job_events (
  id         BIGSERIAL PRIMARY KEY,
  job_id     UUID NOT NULL REFERENCES sync_jobs(id) ON DELETE CASCADE,
  sequence   INT NOT NULL,
  event_type TEXT NOT NULL,
  payload    JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Tenant isolation enforced at DB layer
ALTER TABLE credentials      ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_jobs        ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_job_events  ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_credentials ON credentials
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_jobs ON sync_jobs
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_job_events ON sync_job_events
  USING (job_id IN (
    SELECT id FROM sync_jobs
    WHERE user_id = current_setting('app.user_id', true)::UUID
  ));

CREATE INDEX idx_sync_job_events_job_id_seq ON sync_job_events (job_id, sequence);
