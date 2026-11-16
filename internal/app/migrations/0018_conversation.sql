-- Repair conversation eligibility and Guardian identities in resumable batches
-- after HTTP startup. Existing chronological and conversation indexes suffice.
INSERT INTO maintenance_jobs(name) VALUES('conversation.v1');
