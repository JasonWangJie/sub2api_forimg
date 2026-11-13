-- Independent image quality state. It never changes successful task/billing state.
CREATE TABLE IF NOT EXISTS image_resolution_quality_state (
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 requested_model VARCHAR(255) NOT NULL,
 size_tier VARCHAR(32) NOT NULL,
 failure_count INTEGER NOT NULL DEFAULT 0,
 cycle_id TEXT NOT NULL DEFAULT '',
 cooldown_until TIMESTAMPTZ,
 ignore_before TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
 last_started_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
 last_pass_started_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(account_id, requested_model, size_tier)
);
CREATE TABLE IF NOT EXISTS image_resolution_quality_events (
 id BIGSERIAL PRIMARY KEY,
 observation_id VARCHAR(512) NOT NULL UNIQUE,
 account_id BIGINT NOT NULL,
 requested_model VARCHAR(255) NOT NULL,
 size_tier VARCHAR(32) NOT NULL,
 outcome VARCHAR(16) NOT NULL,
 observation JSONB NOT NULL,
 min_area_percent INTEGER NOT NULL,
 cycle_id TEXT NOT NULL DEFAULT '',
 cooldown_until TIMESTAMPTZ,
 mail_status VARCHAR(16) NOT NULL DEFAULT 'none',
 recipients JSONB NOT NULL DEFAULT '[]',
 delivered JSONB NOT NULL DEFAULT '[]',
 attempts INTEGER NOT NULL DEFAULT 0,
 available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 claim_token TEXT,
 claimed_at TIMESTAMPTZ,
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CONSTRAINT image_resolution_outcome CHECK (outcome IN ('pass','mismatch','unknown')),
 CONSTRAINT image_resolution_mail_status CHECK (mail_status IN ('none','pending','sent','failed','skipped'))
);
CREATE INDEX IF NOT EXISTS image_resolution_quality_pending_idx
 ON image_resolution_quality_events(available_at,id) WHERE mail_status='pending';
CREATE INDEX IF NOT EXISTS image_resolution_quality_account_idx
 ON image_resolution_quality_events(account_id,created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS image_resolution_quality_cycle_idx
 ON image_resolution_quality_events(cycle_id) WHERE cycle_id<>'';
CREATE INDEX IF NOT EXISTS image_resolution_quality_retention_idx
 ON image_resolution_quality_events(created_at,id) WHERE mail_status<>'pending' AND claim_token IS NULL;
