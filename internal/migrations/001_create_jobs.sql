CREATE TABLE jobs (
    id text PRIMARY KEY,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    file_path text NOT NULL,
    report jsonb,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);

CREATE INDEX jobs_queue_idx ON jobs (created_at, id) WHERE status = 'queued';
