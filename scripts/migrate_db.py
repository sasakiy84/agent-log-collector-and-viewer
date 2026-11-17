#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.14"
# dependencies = []
# ///
"""Offline, Python-only conversion of a prepared v0.23 database to storage v2.

The complete data directory is published only after byte-level table validation.
No AGrahpite binary, source transcript files, or network connection is needed.
"""
from __future__ import annotations

import argparse
from contextlib import closing
from datetime import datetime, timezone
import hashlib
from itertools import zip_longest
import json
import os
from pathlib import Path
import shutil
import sqlite3
import struct
import tempfile

APPLICATION_ID = 0x414C4732
SCHEMA_VERSION = 1
LEGACY_VERSION = 23
PERFORMANCE = Path(__file__).resolve().parents[1] / "internal/app/performance.sql"
SCHEMA = Path(__file__).resolve().parents[1] / "internal/app/schema.sql"

# Identity names are deliberately listed too: unknown data must never be dropped.
TABLES = {
    "events": "records", "event_keys": "record_keys",
    "event_cards": "record_facts", "timeline_entries": "logical_events",
    "session_catalog": "sessions", "usage_points": "usage_samples",
    "usage_summary": "session_usage", "folder_members": "session_membership",
    "folder_totals": "directory_summaries", "project_totals": "project_summaries",
    "codex_session_facts": "session_evidence", "archives": "archive_jobs",
    "archived_records": "record_tombstones", "archived_usage_records": "usage_tombstones",
    "session_revisions": "session_versions", "archive_stats": "record_totals",
    "archive_sessions": "archive_sessions", "archive_locks": "archive_locks",
    "search_documents": "search_documents", "search_identifiers": "search_identifiers",
    "card_counts": "card_counts", "timeline_counts": "timeline_counts",
    "usage_streams": "usage_streams", "usage_interval_data": "usage_interval_data",
    "usage_groups": "usage_groups", "usage_totals": "usage_totals",
    "event_errors": "event_errors", "timeline_errors": "timeline_errors",
    "agent_calls": "agent_calls", "agent_activity": "agent_activity",
    "skill_events": "skill_events", "skill_read_inputs": "skill_read_inputs",
    "model_facts": "model_facts", "model_calls": "model_calls",
    "processing_buckets": "processing_buckets", "api_keys": "api_keys",
}
PENDING = (
    "cards_dirty", "search_dirty", "presentation_dirty", "read_model_dirty",
    "folder_dirty", "trace_dirty", "model_dirty", "model_trace_dirty",
    "session_visibility_dirty", "skill_reads_dirty", "usage_sessions_dirty",
    "usage_dirty", "archive_stats_pending",
)
VIRTUAL = ("event_search", "usage_interval_index")
INBOX_TABLES = {"schema_migrations", "ingest_batches", "ingest_sessions", "ingest_size",
                "watch_files", "watch_records"}


class MigrationError(Exception):
    pass


def quote(name: str) -> str:
    return '"' + name.replace('"', '""') + '"'


def readonly(path: Path) -> sqlite3.Connection:
    if not path.is_file():
        raise MigrationError(f"Source file does not exist: {path}")
    # Do not use immutable=1: an offline source may still have committed WAL pages.
    db = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    db.execute("PRAGMA query_only=ON")
    db.execute("BEGIN")
    return db


def tables(db: sqlite3.Connection) -> set[str]:
    return {name for (name,) in db.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")}


def columns(db: sqlite3.Connection, table: str, generated: bool = False) -> list[str]:
    return [row[1] for row in db.execute(f"PRAGMA table_xinfo({quote(table)})")
            if row[6] == 0 or generated and row[6] in (2, 3)]


def implicit_rowid(db: sqlite3.Connection, table: str) -> bool:
    # usage_interval_data/index.id refer to usage_samples.rowid. Other rowid
    # tables retain their positions too; a text PK does not alias SQLite rowid.
    definition = next(row for row in db.execute("PRAGMA table_list") if row[1] == table)
    if definition[4]:  # WITHOUT ROWID
        return False
    primary = [row for row in db.execute(f"PRAGMA table_xinfo({quote(table)})") if row[5]]
    return not (len(primary) == 1 and primary[0][2].upper() == "INTEGER")


def table_digest(db: sqlite3.Connection, table: str) -> tuple[int, str]:
    info = list(db.execute(f"PRAGMA table_xinfo({quote(table)})"))
    selected = columns(db, table, generated=True)
    if implicit_rowid(db, table):
        selected.insert(0, "rowid")
    primary = [row[1] for row in sorted(info, key=lambda row: row[5]) if row[5]]
    # All persistent application/shadow tables have a PK, except FTS docsize in
    # some SQLite versions, where the implicit rowid is the stable ordering key.
    order = ",".join(map(quote, primary)) if primary else "rowid"
    query = f"SELECT {','.join(map(quote, selected))} FROM {quote(table)} ORDER BY {order}"
    digest = hashlib.sha256()
    count = 0
    for row in db.execute(query):
        digest.update(b"R")
        for value in row:
            if value is None:
                tag, data = b"N", b""
            elif isinstance(value, bytes):
                tag, data = b"B", value
            elif isinstance(value, str):
                tag, data = b"T", value.encode("utf-8")
            elif isinstance(value, int):
                tag, data = b"I", str(value).encode("ascii")
            elif isinstance(value, float):
                tag, data = b"F", struct.pack(">d", value)
            else:
                raise MigrationError(f"Unsupported SQLite value in {table}")
            digest.update(tag)
            digest.update(struct.pack(">Q", len(data)))
            digest.update(data)
        count += 1
    return count, digest.hexdigest()


def integrity(db: sqlite3.Connection, label: str) -> None:
    result = db.execute("PRAGMA integrity_check").fetchall()
    if result != [("ok",)]:
        raise MigrationError(f"{label}: SQLite integrity_check failed")
    if db.execute("PRAGMA foreign_key_check").fetchone() is not None:
        raise MigrationError(f"{label}: foreign_key_check failed")


def check_legacy(db: sqlite3.Connection) -> dict[str, str]:
    if db.execute("PRAGMA application_id").fetchone()[0] != 0:
        raise MigrationError("Source is not the legacy storage format")
    if "schema_migrations" not in tables(db):
        raise MigrationError("Source has no v0.23 migration history")
    versions = [row[0] for row in db.execute("SELECT version FROM schema_migrations ORDER BY version")]
    if versions != list(range(1, LEGACY_VERSION + 1)):
        raise MigrationError("Source must use the complete v0.23.0 schema (migrations 1–23)")
    jobs = {name: (complete, error) for name, complete, error in db.execute(
        "SELECT name,complete,error FROM maintenance_jobs")}
    # v0.22 replaced skills.v1 with skills.reads.v2, without completing the
    # retired checkpoint. A normally prepared v0.23 DB can retain that row at 0.
    superseded = {"skills.v1"} if jobs.get("skills.reads.v2") == (1, "") else set()
    incomplete = sorted(name for name, state in jobs.items()
                        if state != (1, "") and name not in superseded)
    if incomplete:
        raise MigrationError("Legacy database preparation is incomplete: " + ", ".join(incomplete)
                             + ". Finish preparation with the old service before stopping it.")
    for name in PENDING:
        if db.execute(f"SELECT 1 FROM {quote(name)} LIMIT 1").fetchone():
            raise MigrationError(f"Legacy database has pending work in {name}. "
                                 "Finish preparation with the old service before stopping it.")
    # Shadows store exact FTS postings and R-tree coordinates; copying them
    # preserves Japanese segmentation/positions without a second tokenizer.
    mapping = dict(TABLES)
    for virtual in VIRTUAL:
        names = {row[1] for row in db.execute("PRAGMA table_list")
                 if row[2] == "shadow" and row[1].startswith(virtual + "_")}
        if not names:
            raise MigrationError(f"Missing SQLite shadow tables for {virtual}")
        mapping.update((name, name) for name in names)
    expected = set(mapping) | set(PENDING) | set(VIRTUAL) | {"schema_migrations", "maintenance_jobs"}
    actual = tables(db)
    if actual != expected:
        raise MigrationError(f"Unexpected legacy schema; missing={sorted(expected-actual)}, "
                             f"extra={sorted(actual-expected)}")
    integrity(db, "Legacy database")
    return mapping


def copy_main(source: Path, legacy: sqlite3.Connection, destination: Path,
              mapping: dict[str, str]) -> dict:
    schema = SCHEMA.read_bytes()
    with closing(sqlite3.connect(destination, uri=True)) as db:
        os.chmod(destination, 0o600)
        db.execute("PRAGMA auto_vacuum=INCREMENTAL")
        db.executescript(schema.decode("utf-8"))
        indexes = db.execute("SELECT name,sql FROM sqlite_master WHERE type='index' AND sql IS NOT NULL").fetchall()
        triggers = db.execute("SELECT name,sql FROM sqlite_master WHERE type='trigger'").fetchall()
        # Build secondary indexes once after the bulk copy; updating all of them
        # for every historical row needlessly multiplies migration I/O.
        for name, _ in indexes:
            db.execute(f"DROP INDEX {quote(name)}")
        for name, _ in triggers:
            db.execute(f"DROP TRIGGER {quote(name)}")
        db.commit()
        db.execute("PRAGMA foreign_keys=OFF")
        db.execute("ATTACH DATABASE ? AS legacy", (source.resolve().as_uri() + "?mode=ro",))
        db.execute("BEGIN")
        for old, new in mapping.items():
            selected = columns(db, new)
            # Old generated project columns become stored columns in v2. Read
            # their evaluated values without reinterpreting UI preview JSON.
            if columns(legacy, old, generated=True) != columns(db, new, generated=True):
                raise MigrationError(f"Column mismatch: {old} → {new}")
            if implicit_rowid(legacy, old) != implicit_rowid(db, new):
                raise MigrationError(f"Rowid layout mismatch: {old} → {new}")
            if implicit_rowid(legacy, old):
                selected.insert(0, "rowid")
            names = ",".join(map(quote, selected))
            db.execute(f"DELETE FROM {quote(new)}")
            db.execute(f"INSERT INTO {quote(new)}({names}) SELECT {names} FROM legacy.{quote(old)}")
        db.execute("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,"
                   "checksum TEXT NOT NULL,applied_at TEXT NOT NULL)")
        db.execute("INSERT INTO schema_migrations VALUES(1,'0001_storage_v2',?,?)", (
            hashlib.sha256(schema).hexdigest(), datetime.now(timezone.utc).isoformat()))
        for _, sql in indexes + triggers:
            db.execute(sql)
        # Offline conversion has the resources to publish useful planner
        # statistics together with the new indexes, before production starts.
        db.execute("ANALYZE main")
        db.commit()
        db.execute("DETACH DATABASE legacy")
    # Reopen to discard FTS/R-tree module caches from empty-schema creation.
    report = {}
    with closing(sqlite3.connect(destination)) as db:
        db.execute("PRAGMA foreign_keys=ON")
        integrity(db, "New database")
        db.execute("INSERT INTO event_search(event_search,rank) VALUES('integrity-check',0)")
        db.commit()
        for old, new in mapping.items():
            before = table_digest(legacy, old)
            after = table_digest(db, new)
            if before != after:
                raise MigrationError(f"Data verification failed: {old} → {new}")
            report[new] = {"source": old, "rows": after[0], "sha256": after[1]}
            print(f"Verified {old} → {new}: {after[0]:,} rows", flush=True)
        # Verify the old representation first, then independently verify each
        # transformed field. The retired presentation_ready flag is deliberately
        # removed; original raw JSON and all original ID keys remain unchanged.
        if db.execute("SELECT 1 FROM search_identifiers i WHERE NOT EXISTS(SELECT 1 FROM search_documents d WHERE d.event_id=i.event_id) LIMIT 1").fetchone():
            raise MigrationError("Identifier has no corresponding search document")
        db.executescript(PERFORMANCE.read_text())
        after_ids = db.execute("SELECT event_id,session,key,value FROM search_identifiers ORDER BY event_id,key,value")
        # Streaming comparison avoids retaining a second materialized copy.
        before_ids = legacy.execute("SELECT event_id,session,key,value FROM search_identifiers ORDER BY event_id,key,value")
        if any(before != after for before, after in zip_longest(before_ids, after_ids)):
            raise MigrationError("Identifier dictionary verification failed")
        # Only the new hash changes. Existing UPDATE triggers intentionally
        # invalidate evidence even for metadata updates, so suppress them during
        # this offline, single-writer operation and restore the exact definitions.
        hash_triggers = db.execute("SELECT name,sql FROM sqlite_master WHERE type='trigger' AND tbl_name='records'").fetchall()
        for name, _ in hash_triggers:
            db.execute(f"DROP TRIGGER {quote(name)}")
        cursor = db.execute("SELECT id,raw FROM records ORDER BY id")
        while batch := cursor.fetchmany(1000):
            db.executemany("UPDATE records SET raw_hash=? WHERE id=?", ((hashlib.sha256(raw).digest(), key) for key, raw in batch))
        for _, definition in hash_triggers:
            db.execute(definition)
        db.execute("INSERT INTO schema_migrations VALUES(2,'0002_storage_performance',?,?)", (
            hashlib.sha256(PERFORMANCE.read_bytes()).hexdigest(), datetime.now(timezone.utc).isoformat()))
        db.execute("ANALYZE main")
        db.commit()
        if db.execute("SELECT 1 FROM work_items LIMIT 1").fetchone():
            raise MigrationError("New database unexpectedly contains pending work")
        if db.execute("PRAGMA application_id").fetchone()[0] != APPLICATION_ID or \
                db.execute("PRAGMA user_version").fetchone()[0] != SCHEMA_VERSION:
            raise MigrationError("New database schema identity does not match the runtime")
        # Reclaim the dropped indexes/ID representation without changing hidden
        # rowids referenced by usage_interval_data. Generic VACUUM is unsuitable.
        while db.execute("PRAGMA freelist_count").fetchone()[0]:
            db.execute("PRAGMA incremental_vacuum(10000)").fetchall()
            db.commit()
        integrity(db, "Optimized database")
        db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    return report


def copy_inbox(legacy: sqlite3.Connection, destination: Path) -> dict:
    names = tables(legacy)
    if names != INBOX_TABLES:
        raise MigrationError(f"Unexpected inbox schema: {sorted(names)}")
    integrity(legacy, "Legacy inbox")
    history = legacy.execute("SELECT version,name,checksum FROM schema_migrations ORDER BY version").fetchall()
    if history != [(1, "0001_durable_inbox", "3dbed5d4ce655e162192c0073caf4b7c66f09c83e79cc853d28070767aa636ca")]:
        raise MigrationError("Legacy inbox migration history does not match v0.23.0")
    batches, size = legacy.execute("SELECT batches,bytes FROM ingest_size WHERE id=1").fetchone()
    actual = legacy.execute("SELECT COUNT(*),COALESCE(SUM(length(data)),0) FROM ingest_batches").fetchone()
    if (batches, size) != actual:
        raise MigrationError("Legacy inbox counters do not match its queued batches")
    with closing(sqlite3.connect(destination)) as db:
        os.chmod(destination, 0o600)
        legacy.backup(db)
        integrity(db, "New inbox")
        report = {}
        # AUTOINCREMENT's high-water mark matters even if the inbox is empty.
        for table in sorted(names | {"sqlite_sequence"}):
            before = table_digest(legacy, table)
            after = table_digest(db, table)
            if before != after:
                raise MigrationError(f"Inbox verification failed: {table}")
            report[table] = {"rows": after[0], "sha256": after[1]}
        db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    return report


def file_digest(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def copy_archives(legacy: sqlite3.Connection, source: Path, destination: Path) -> dict:
    report = {}
    if source.exists():
        if not source.is_dir() or source.is_symlink():
            raise MigrationError(f"Archive source must be a directory: {source}")
        for path in sorted(source.rglob("*")):
            if path.is_symlink():
                raise MigrationError(f"Archive source contains a symlink: {path}")
            relative = path.relative_to(source)
            target = destination / relative
            if path.is_dir():
                target.mkdir(parents=True, exist_ok=True, mode=0o700)
                continue
            if not path.is_file():
                raise MigrationError(f"Archive source contains a non-regular file: {path}")
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            before = path.stat()
            shutil.copyfile(path, target)
            os.chmod(target, 0o600)
            digest = file_digest(target)
            after = path.stat()
            if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns) or \
                    digest != file_digest(path):
                raise MigrationError(f"Archive changed during migration: {path}")
            report[str(relative)] = {"bytes": target.stat().st_size, "sha256": digest}
    for archive, status, size, expected, verified in legacy.execute(
            "SELECT id,status,archive_bytes,sha256,verified_at FROM archives"):
        if status not in ("ready", "deleting"):
            continue
        filename = archive + ".zip"
        info = report.get(filename)
        if info is None and status == "deleting" and verified:
            # A confirmed deletion may have removed its verified ZIP already.
            continue
        if info is None or info["bytes"] != size or info["sha256"] != expected:
            raise MigrationError(f"Required archive ZIP is missing or corrupt: {filename}")
    return report


def run(source: Path, output: Path, inbox: Path | None = None,
        archives: Path | None = None) -> Path:
    if sqlite3.sqlite_version_info < (3, 43, 0):
        raise MigrationError("SQLite >=3.43 with FTS5 and R-tree is required; "
                             "run with uv run --python 3.14 scripts/migrate_db.py")
    source = source.resolve()
    inbox = (inbox or Path(str(source) + ".ingest.db")).resolve()
    archives = (archives or source.parent / "archives").absolute()
    output = output.absolute()
    if output.exists() or output.is_symlink():
        raise MigrationError(f"Output already exists; refusing to overwrite: {output}")
    if output.resolve().is_relative_to(archives.resolve()):
        raise MigrationError("Output must not be inside the source archives directory")
    if not SCHEMA.is_file():
        raise MigrationError(f"New schema file is missing: {SCHEMA}")
    output.parent.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix="." + output.name + ".migrating-", dir=output.parent))
    try:
        with closing(readonly(source)) as old, closing(readonly(inbox)) as queue:
            mapping = check_legacy(old)
            report = {
                "format": "agentlog.storage-migration", "version": 1,
                "source_version": "0.23.0", "application_id": APPLICATION_ID,
                "schema_version": SCHEMA_VERSION,
                "schema_sha256": hashlib.sha256(SCHEMA.read_bytes()).hexdigest(),
                "migrations": [
                    {"version": 1, "name": "0001_storage_v2", "sha256": hashlib.sha256(SCHEMA.read_bytes()).hexdigest()},
                    {"version": 2, "name": "0002_storage_performance", "sha256": hashlib.sha256(PERFORMANCE.read_bytes()).hexdigest()},
                ],
                "created_at": datetime.now(timezone.utc).isoformat(),
                "tables": copy_main(source, old, stage / "agentlog.db", mapping),
                "inbox": copy_inbox(queue, stage / "agentlog.db.ingest.db"),
                "archives": copy_archives(old, archives, stage / "archives"),
            }
        report_file = stage / "migration-report.json"
        report_file.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        os.chmod(report_file, 0o600)
        # Both connections have closed and checkpointed before publication.
        # A successful output is nonempty, so two migrators cannot replace one another.
        if output.exists() or output.is_symlink():
            raise MigrationError(f"Output appeared during migration: {output}")
        stage.rename(output)
        print(f"Migration complete: {output}", flush=True)
        return output
    except BaseException:
        shutil.rmtree(stage, ignore_errors=True)
        raise


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path, help="Offline v0.23.0 agentlog.db")
    parser.add_argument("--output", required=True, type=Path, help="New, nonexistent output data directory")
    parser.add_argument("--inbox", type=Path, help="Old inbox DB (default: SOURCE.ingest.db)")
    parser.add_argument("--archives", type=Path, help="Old archives directory (default: SOURCE parent/archives)")
    args = parser.parse_args()
    try:
        run(args.source, args.output, args.inbox, args.archives)
    except (MigrationError, sqlite3.Error, OSError) as error:
        parser.exit(1, f"Migration failed: {error}\n")


if __name__ == "__main__":
    main()
