-- Reclassify page-context messages and repair cached titles once, in bounded
-- background batches. Existing completed jobs and original events stay intact.
INSERT INTO maintenance_jobs(name) VALUES('codex_page_messages.v1');
