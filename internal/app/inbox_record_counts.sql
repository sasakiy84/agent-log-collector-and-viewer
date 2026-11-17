-- Read existing queued payloads once; keep the large batch blobs untouched.
CREATE TABLE ingest_record_counts(
 batch INTEGER PRIMARY KEY REFERENCES ingest_batches(id) ON DELETE CASCADE,
 records INTEGER NOT NULL CHECK(records>=0)
);
ALTER TABLE ingest_size ADD COLUMN records INTEGER NOT NULL DEFAULT 0;
INSERT INTO ingest_record_counts(batch,records)
 SELECT id,json_array_length(CAST(data AS TEXT)) FROM ingest_batches;
UPDATE ingest_size SET records=(SELECT COALESCE(SUM(records),0) FROM ingest_record_counts) WHERE id=1;
CREATE TRIGGER ingest_records_added AFTER INSERT ON ingest_record_counts BEGIN
 UPDATE ingest_size SET records=records+NEW.records WHERE id=1;
END;
CREATE TRIGGER ingest_records_removed AFTER DELETE ON ingest_record_counts BEGIN
 UPDATE ingest_size SET records=records-OLD.records WHERE id=1;
END;
