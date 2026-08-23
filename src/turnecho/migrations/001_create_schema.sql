CREATE TABLE turnecho_jobs (
    id TEXT PRIMARY KEY,
    host TEXT NOT NULL CHECK (host <> ''),
    session_id TEXT NOT NULL CHECK (session_id <> ''),
    turn_id TEXT NOT NULL CHECK (turn_id <> ''),
    message TEXT NOT NULL CHECK (message <> ''),
    voice TEXT NOT NULL
        CHECK (voice IN ('Bella', 'Jasper', 'Luna', 'Bruno', 'Rosie', 'Hugo', 'Kiki', 'Leo')),
    speed REAL NOT NULL CHECK (speed >= 0.5 AND speed <= 2.0),
    processing_status TEXT NOT NULL CHECK (processing_status <> ''),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    started_at INTEGER,
    completed_at INTEGER,
    error_message TEXT,
    UNIQUE(host, session_id, turn_id)
);

CREATE INDEX idx_turnecho_jobs_status_queue
ON turnecho_jobs(processing_status, created_at);
