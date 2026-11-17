-- Keep the shipped baseline immutable. Existing explicit document rowids
-- remain the FTS identity and also provide compact identifier postings.
ALTER TABLE records ADD COLUMN raw_hash BLOB;
DROP INDEX cards_agent_candidates;
DROP INDEX cards_title_backfill;
DROP INDEX cards_presentation_pending;
ALTER TABLE record_facts DROP COLUMN presentation_ready;

CREATE TABLE identifier_values(
 id INTEGER PRIMARY KEY,key TEXT NOT NULL,value TEXT NOT NULL,UNIQUE(value,key));
CREATE TABLE identifier_postings(
 document_id INTEGER NOT NULL REFERENCES search_documents(rowid) ON DELETE CASCADE,
 identifier_id INTEGER NOT NULL REFERENCES identifier_values(id),session TEXT NOT NULL,
 PRIMARY KEY(document_id,identifier_id)) WITHOUT ROWID;
CREATE INDEX identifier_lookup ON identifier_postings(identifier_id,session);
INSERT INTO identifier_values(key,value) SELECT DISTINCT key,value FROM search_identifiers;
INSERT INTO identifier_postings
 SELECT d.rowid,v.id,i.session FROM search_identifiers i
 JOIN search_documents d ON d.event_id=i.event_id
 JOIN identifier_values v ON v.value=i.value AND v.key=i.key;
-- Refuse missing search documents rather than silently dropping ID evidence.
CREATE TEMP TABLE identifier_copy_check(missing INTEGER CHECK(missing=0));
INSERT INTO identifier_copy_check SELECT
 (SELECT COUNT(*) FROM search_identifiers)-(SELECT COUNT(*) FROM identifier_postings);
DROP TABLE identifier_copy_check;
DROP TABLE search_identifiers;
CREATE VIEW search_identifiers AS
 SELECT d.event_id,p.session,v.key,v.value FROM identifier_values v
 JOIN identifier_postings p ON p.identifier_id=v.id
 JOIN search_documents d ON d.rowid=p.document_id;
-- Lookup only the affected dictionary entry; do not scan the whole dictionary.
CREATE TRIGGER identifier_prune AFTER DELETE ON identifier_postings BEGIN
 DELETE FROM identifier_values WHERE id=OLD.identifier_id
 AND NOT EXISTS(SELECT 1 FROM identifier_postings WHERE identifier_id=OLD.identifier_id); END;

DROP VIEW pending_storage;
DELETE FROM work_items WHERE kind='storage';
