-- Rebuild just the full-text postings using the stored originals. The
-- application clears this marker in the transaction that publishes them.
INSERT INTO work_items(kind,scope,key) VALUES ('search_content_rebuild','','');
