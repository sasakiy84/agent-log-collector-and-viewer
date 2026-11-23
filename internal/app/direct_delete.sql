ALTER TABLE archive_jobs ADD COLUMN direct_delete INTEGER NOT NULL DEFAULT 0;
CREATE INDEX maintenance_jobs_latest ON archive_jobs(direct_delete,created_at DESC,id DESC);
