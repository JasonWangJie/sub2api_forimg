CREATE TABLE IF NOT EXISTS async_image_retry_versions (
    revision BIGINT PRIMARY KEY CHECK (revision > 0),
    policy JSONB NOT NULL,
    actor_id BIGINT NOT NULL CHECK (actor_id >= 0),
    operation TEXT NOT NULL CHECK (operation IN ('initial', 'save', 'rollback')),
    rollback_from BIGINT REFERENCES async_image_retry_versions(revision),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS async_image_retry_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    revision BIGINT NOT NULL REFERENCES async_image_retry_versions(revision)
);

INSERT INTO async_image_retry_versions (revision, policy, actor_id, operation)
VALUES (1, '{"revision":1,"pause_generation_retries":false,"rules":[]}', 0, 'initial')
ON CONFLICT (revision) DO NOTHING;

INSERT INTO async_image_retry_policy (singleton, revision)
VALUES (TRUE, 1)
ON CONFLICT (singleton) DO NOTHING;

-- Error samples retain a bounded snapshot after the underlying task expires.
CREATE TABLE IF NOT EXISTS async_image_retry_events (
    id BIGSERIAL PRIMARY KEY,
    task_id VARCHAR(64) NOT NULL,
    task_version BIGINT NOT NULL,
    failure_id TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision >= 0),
    rule_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    fact JSONB NOT NULL,
    decision JSONB NOT NULL,
    requires_pending BOOLEAN NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    generation_started_at TIMESTAMPTZ,
    target_account_id BIGINT,
    final_status VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, failure_id, revision, rule_id, step_id)
);

CREATE INDEX IF NOT EXISTS async_image_retry_events_occurred_idx
    ON async_image_retry_events (occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS async_image_retry_events_fingerprint_idx
    ON async_image_retry_events (fingerprint, occurred_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS async_image_retry_state (
    task_id VARCHAR(64) PRIMARY KEY REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    event_id BIGINT REFERENCES async_image_retry_events(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'started', 'manual')),
    due_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS async_image_retry_state_event_idx
    ON async_image_retry_state (event_id) WHERE event_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS async_image_retry_usage (
    task_id VARCHAR(64) REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    rule_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    used_count INTEGER NOT NULL CHECK (used_count > 0),
    PRIMARY KEY (task_id, rule_id, step_id)
);

-- Record initial and pre-upgrade sends that do not have a decision event.
CREATE TABLE IF NOT EXISTS async_image_retry_task_dispatches (
    task_id VARCHAR(64) REFERENCES async_image_tasks(task_id) ON DELETE CASCADE,
    task_version BIGINT NOT NULL,
    revision BIGINT REFERENCES async_image_retry_versions(revision),
    account_id BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (task_id, task_version)
);

CREATE OR REPLACE FUNCTION async_image_retry_snapshot_final_status() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE async_image_retry_events SET final_status = OLD.status WHERE task_id = OLD.task_id;
    ELSIF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN (
        'succeeded', 'failed', 'execution_unknown', 'storage_failed', 'billing_failed', 'expired'
    ) THEN
        UPDATE async_image_retry_events SET final_status = NEW.status WHERE task_id = NEW.task_id;
    ELSIF NEW.status IS DISTINCT FROM OLD.status AND OLD.status IN ('storage_failed', 'billing_failed') THEN
        UPDATE async_image_retry_events SET final_status = '' WHERE task_id = NEW.task_id;
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS async_image_retry_snapshot_final_status_trigger ON async_image_tasks;
CREATE TRIGGER async_image_retry_snapshot_final_status_trigger
    AFTER UPDATE OF status OR DELETE ON async_image_tasks
    FOR EACH ROW
    EXECUTE FUNCTION async_image_retry_snapshot_final_status();
