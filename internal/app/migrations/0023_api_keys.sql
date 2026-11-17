-- Keys are intentionally recoverable for the owner's Settings page. Only the
-- indexed digest participates in request authentication; keys grant read access.
CREATE TABLE api_keys(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,key TEXT NOT NULL,
 key_hash BLOB NOT NULL UNIQUE,created_at TEXT NOT NULL);
