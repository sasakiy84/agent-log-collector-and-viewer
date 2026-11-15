-- Repair only titles affected by a newly recognized Codex application message.
-- Original payload extraction happens in checkpointed background batches.
INSERT INTO maintenance_jobs(name) VALUES('codex_app_titles.v1');
