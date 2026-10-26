-- Value validation (voice names, speed range, non-blank fields) lives in the
-- application (queue.Insert); only identity and shape are constrained here.
CREATE TABLE turnecho_jobs (
    id TEXT PRIMARY KEY,
    host TEXT NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    message TEXT NOT NULL,
    voice TEXT NOT NULL,
    speed REAL NOT NULL,
    processing_status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    error_message TEXT,
    UNIQUE(host, session_id, turn_id)
);

CREATE INDEX idx_turnecho_jobs_status_queue
ON turnecho_jobs(processing_status, created_at);
