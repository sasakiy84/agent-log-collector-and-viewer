#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.14"
# dependencies = []
# ///
"""Focused offline migration integration tests; no Go executable is required."""
from contextlib import closing, redirect_stdout
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("migration", ROOT / "migrate_db.py")
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)
RAW = b'{ "record": {"body":{"stringValue":"\\u65e5\\u672c\\u8a9e"}}, "resource": {}, "scope": {} }'


def fixture(directory: Path) -> Path:
    directory.mkdir()
    path = directory / "agentlog.db"
    with closing(sqlite3.connect(path)) as db:
        db.executescript((ROOT / "testdata/legacy_storage_v023.sql").read_text())
        # Populate a frozen, already-prepared storage snapshot rather than
        # duplicating the old Go ingestion/projection implementation in Python.
        for (name,) in db.execute("SELECT name FROM sqlite_master WHERE type='trigger'").fetchall():
            db.execute('DROP TRIGGER "' + name + '"')
        event = ("import:demo", "codex:demo", "", "codex", "logs", "2026-11-17T01:00:00.000000000Z",
                 "", "", "", 4, "import", "user", "", "gen_ai.user.message", RAW)
        db.execute("INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", event)
        db.execute("INSERT INTO event_keys VALUES('import:demo','message',?)", ("ab" * 32,))
        detail = json.dumps({"kind": "user", "name": "gen_ai.user.message", "title": "User", "blocks": [
            {"label": "Request", "text": "日本語検索を高速化して"}], "cwd": "/work/demo", "project": "demo"}).encode()
        db.execute("""INSERT INTO event_cards(id,session,origin,source,parent,time,sequence,is_user,signal,
 trace,span,parent_span,detail,prompt_title,presentation_ready,activity,kind,is_error,has_skill,conversation,turn)
 VALUES('import:demo','codex:demo','import','codex','','2026-11-17T01:00:00.000000000Z',4,1,
 'logs','','','',?,'日本語検索を高速化して',1,1,'user',0,1,1,'turn-one')""", (detail,))
        db.execute("""INSERT INTO timeline_entries VALUES('import:demo','codex:demo','codex','',
 '2026-11-17T01:00:00.000000000Z','2026-11-17T01:00:00.000000000Z',4,1,'import:demo','',1,'user',0,1,1)""")
        db.execute("""INSERT INTO session_catalog(session,source,parent,count,start,end,import_count,
 import_start,import_end,otel_count,otel_start,otel_end,preview,internal)
 VALUES('codex:demo','codex','',1,'2026-11-17T01:00:00.000000000Z','2026-11-17T01:00:00.000000000Z',1,
 '2026-11-17T01:00:00.000000000Z','2026-11-17T01:00:00.000000000Z',0,'','',?,0)""", (detail,))
        db.execute("INSERT INTO session_revisions VALUES('codex:demo',123)")
        db.execute("INSERT INTO archive_stats VALUES('codex:demo',1,?)", (len(RAW),))
        db.execute("INSERT INTO card_counts VALUES('codex:demo','import','codex','',1)")
        db.execute("INSERT INTO timeline_counts VALUES('codex:demo',1)")
        db.execute("INSERT INTO search_documents VALUES(42,'import:demo','codex:demo')")
        db.execute("INSERT INTO event_search(rowid,text) VALUES(42,'日本語 検索 高速 化')")
        db.execute("INSERT INTO search_identifiers VALUES('import:demo','codex:demo','message.id','message-1')")
        db.execute("INSERT INTO skill_events VALUES('import:demo','codex:demo','import','coding','','repo','','injection','group-one','2026-11-17T01:00:00.000000000Z','/work/demo/SKILL.md','coding')")
        db.execute("INSERT INTO usage_points(rowid,id,session,origin,basis,group_key,series,start,end,time,sequence,values_json) VALUES(87,'import:demo','codex:demo','import','logs','sample-one','','','','2026-11-17T01:00:00.000000000Z',4,?)", ('{"input_tokens":"9007199254740993","cost_usd":"0.000000001"}',))
        db.execute("INSERT INTO usage_streams VALUES(7,'codex:demo','import','series-one')")
        db.execute("INSERT INTO usage_interval_data VALUES(87,'import:demo',7,'00000000000000000001','00000000000000000002',0,0,0,0,1,2)")
        db.execute("INSERT INTO usage_interval_index VALUES(87,7,7,0,0,0,0,1,2)")
        db.execute("INSERT INTO api_keys VALUES('key-one','移行前のキー','alk_synthetic-secret',?,'2026-11-17T00:00:00.000000000Z')", (hashlib.sha256(b'alk_synthetic-secret').digest(),))
        db.execute("INSERT INTO archived_records VALUES(?,?)", (b'i' * 32, b'r' * 32))
        db.execute("INSERT INTO archived_usage_records VALUES(?,?)", (b'i' * 32, b'r' * 32))
        archive = directory / "archives"
        archive.mkdir()
        with zipfile.ZipFile(archive / "archive-one.zip", "w") as stream:
            stream.writestr("manifest.json", '{"format":"agentlog.session-archive","version":1}')
        payload = (archive / "archive-one.zip").read_bytes()
        db.execute("INSERT INTO archives(id,status,created_at,cutoff,session_count,record_count,raw_bytes,archive_bytes,sha256,download_at) VALUES('archive-one','ready','2026-11-17T00:00:00.000000000Z','2026-10-17T00:00:00.000000000Z',1,1,?,?,?,'2026-11-17T00:01:00.000000000Z')", (len(RAW), len(payload), hashlib.sha256(payload).hexdigest()))
        db.execute("INSERT INTO archive_sessions VALUES('archive-one','codex:demo','codex',123,1,?,'2026-11-17T01:00:00.000000000Z','/work/demo')", (len(RAW),))
        db.commit()
    with closing(sqlite3.connect(str(path) + ".ingest.db")) as db:
        db.executescript((ROOT / "testdata/legacy_inbox_v023.sql").read_text())
        data = b'[{"id":"new-record","raw":{"record":{"n":9007199254740993}}}]'
        db.execute("INSERT INTO ingest_batches(id,data) VALUES(999,?)", (data,))
        db.execute("INSERT INTO ingest_sessions VALUES(999,'codex:pending')")
        db.execute("INSERT INTO ingest_batches(id,data) VALUES(1000,'[]')")
        db.execute("DELETE FROM ingest_batches WHERE id=1000")
        db.execute("INSERT INTO watch_files VALUES('opaque-key',?)", (b'\x00\x01\xff',))
        db.execute("INSERT INTO watch_records VALUES('opaque-key','native-import-id',?)", (b'h' * 32,))
        db.commit()
    return path


class OfflineMigrationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.source = fixture(self.root / "old")
        self.output = self.root / "new"

    def migrate(self):
        with redirect_stdout(io.StringIO()):
            return migration.run(self.source, self.output)

    def test_complete_data_directory_and_committed_wal(self):
        # A read-only connection must include committed WAL pages, not just the
        # main file. Retain this connection to keep those pages in the WAL.
        with closing(sqlite3.connect(self.source)) as old:
            old.execute("PRAGMA journal_mode=WAL")
            old.execute("PRAGMA wal_autocheckpoint=0")
            old.execute("UPDATE api_keys SET name='WAL内の名前'")
            old.commit()
            before = hashlib.sha256(self.source.read_bytes()).digest()
            wal = Path(str(self.source) + "-wal")
            wal_before = hashlib.sha256(wal.read_bytes()).digest()
            self.migrate()
            self.assertEqual(hashlib.sha256(self.source.read_bytes()).digest(), before)
            self.assertEqual(hashlib.sha256(wal.read_bytes()).digest(), wal_before)
        with closing(sqlite3.connect(self.output / "agentlog.db")) as db:
            self.assertEqual(db.execute("SELECT raw FROM records").fetchone()[0], RAW)
            self.assertEqual(db.execute("SELECT name,key FROM api_keys").fetchone(), ('WAL内の名前','alk_synthetic-secret'))
            self.assertEqual(db.execute("SELECT rowid FROM event_search WHERE event_search MATCH '日本語 AND 検索'").fetchall(), [(42,)])
            self.assertEqual(db.execute("SELECT rowid FROM event_search WHERE event_search MATCH '\"検索 高速\"'").fetchall(), [(42,)])
            self.assertEqual(db.execute("SELECT id FROM usage_interval_index WHERE min_stream<=7 AND max_stream>=7").fetchall(), [(87,)])
            self.assertEqual(db.execute("SELECT p.id FROM usage_interval_index i JOIN usage_samples p ON p.rowid=i.id").fetchall(), [('import:demo',)])
            self.assertEqual(db.execute("SELECT revision FROM session_versions").fetchone()[0], 123)
            self.assertEqual(db.execute("SELECT project_key FROM sessions").fetchone()[0], 'name:demo')
            self.assertEqual(db.execute("SELECT COUNT(*) FROM usage_tombstones").fetchone()[0], 1)
            self.assertEqual(db.execute("SELECT COUNT(*) FROM work_items").fetchone()[0], 0)
            self.assertEqual(db.execute("PRAGMA application_id").fetchone()[0], migration.APPLICATION_ID)
            self.assertEqual(db.execute("SELECT checksum FROM schema_migrations").fetchone()[0], hashlib.sha256(migration.SCHEMA.read_bytes()).hexdigest())
        with closing(sqlite3.connect(self.output / "agentlog.db.ingest.db")) as db:
            self.assertEqual(db.execute("SELECT batch,session FROM ingest_sessions").fetchone(), (999,'codex:pending'))
            self.assertEqual(db.execute("SELECT value FROM watch_files").fetchone()[0], b'\x00\x01\xff')
            self.assertEqual(db.execute("SELECT hash FROM watch_records").fetchone()[0], b'h' * 32)
            self.assertEqual(db.execute("SELECT seq FROM sqlite_sequence WHERE name='ingest_batches'").fetchone()[0], 1000)
        report = json.loads((self.output / "migration-report.json").read_text())
        self.assertEqual(report['tables']['records']['rows'], 1)
        self.assertEqual((self.output / "archives/archive-one.zip").read_bytes(), (self.source.parent / "archives/archive-one.zip").read_bytes())
        with self.assertRaisesRegex(migration.MigrationError, 'already exists'):
            self.migrate()

    def test_retired_skill_checkpoint_is_covered_by_completed_successor(self):
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("UPDATE maintenance_jobs SET complete=0 WHERE name='skills.v1'")
            db.commit()
        self.migrate()
        self.assertTrue((self.output / "agentlog.db").is_file())

    def test_unfinished_preparation_is_rejected_without_output(self):
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("UPDATE maintenance_jobs SET complete=0 WHERE name='skills.reads.v2'")
            db.commit()
        with self.assertRaisesRegex(migration.MigrationError, 'preparation is incomplete'):
            self.migrate()
        self.assertFalse(self.output.exists())
        self.assertFalse(list(self.root.glob('.new.migrating-*')))

    def test_pending_queue_is_not_mistaken_for_unfinished_preparation(self):
        # Accepted transport batches are migratable even when not yet projected.
        self.migrate()
        with closing(sqlite3.connect(self.output / "agentlog.db.ingest.db")) as db:
            self.assertEqual(db.execute("SELECT batches FROM ingest_size").fetchone()[0], 1)

    def test_pending_projection_and_missing_archive_fail_closed(self):
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("INSERT INTO cards_dirty VALUES('import:demo')")
            db.commit()
        with self.assertRaisesRegex(migration.MigrationError, 'pending work in cards_dirty'):
            self.migrate()
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("DELETE FROM cards_dirty")
            db.commit()
        (self.source.parent / "archives/archive-one.zip").unlink()
        with self.assertRaisesRegex(migration.MigrationError, 'missing or corrupt'):
            self.migrate()
        self.assertFalse(self.output.exists())

    def test_confirmed_deletion_preserves_locks_without_already_removed_zip(self):
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("UPDATE archives SET status='deleting',verified_at='2026-11-17T02:00:00.000000000Z'")
            db.execute("INSERT INTO archive_locks VALUES('codex:demo','archive-one')")
            db.commit()
        (self.source.parent / "archives/archive-one.zip").unlink()
        self.migrate()
        with closing(sqlite3.connect(self.output / "agentlog.db")) as db:
            self.assertEqual(db.execute("SELECT status FROM archive_jobs").fetchone()[0], 'deleting')
            self.assertEqual(db.execute("SELECT session,archive_id FROM archive_locks").fetchone(), ('codex:demo','archive-one'))

    def test_corrupt_inbox_counters_are_not_published(self):
        with closing(sqlite3.connect(str(self.source) + ".ingest.db")) as db:
            db.execute("UPDATE ingest_size SET batches=0")
            db.commit()
        with self.assertRaisesRegex(migration.MigrationError, 'counters do not match'):
            self.migrate()
        self.assertFalse(self.output.exists())

    def test_inbox_history_is_validated_before_publication(self):
        with closing(sqlite3.connect(str(self.source) + ".ingest.db")) as db:
            db.execute("UPDATE schema_migrations SET checksum='modified'")
            db.commit()
        with self.assertRaisesRegex(migration.MigrationError, 'inbox migration history'):
            self.migrate()
        self.assertFalse(self.output.exists())

    def test_unknown_tables_missing_inbox_and_nested_output_are_rejected(self):
        with self.assertRaisesRegex(migration.MigrationError, "inside the source archives"):
            with redirect_stdout(io.StringIO()):
                migration.run(self.source, self.source.parent / "archives/new")
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("CREATE TABLE unexpected(value BLOB)")
            db.commit()
        with self.assertRaisesRegex(migration.MigrationError, 'Unexpected legacy schema'):
            self.migrate()
        with closing(sqlite3.connect(self.source)) as db:
            db.execute("DROP TABLE unexpected")
            db.commit()
        Path(str(self.source)+'.ingest.db').unlink()
        with self.assertRaisesRegex(migration.MigrationError, 'Source file does not exist'):
            self.migrate()
        self.assertFalse(self.output.exists())


if __name__ == '__main__':
    unittest.main()
