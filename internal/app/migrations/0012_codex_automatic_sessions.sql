-- App-generated review/suggestion conversations can be persistent and have no
-- temporary RPC marker. Preserve evidence separately from real user requests.
ALTER TABLE codex_session_facts ADD COLUMN automatic INTEGER NOT NULL DEFAULT 0;
ALTER TABLE codex_session_facts ADD COLUMN human INTEGER NOT NULL DEFAULT 0;
-- Ordinary conversations can contain many user messages. Existence checks
-- must not aggregate every prompt again after each appended event.
DROP INDEX codex_session_facts_session;
CREATE INDEX codex_session_metadata ON codex_session_facts(session)
 WHERE ephemeral<>0 OR persistent<>0 OR temporary<>0;
CREATE INDEX codex_session_automatic ON codex_session_facts(session) WHERE automatic<>0;
CREATE INDEX codex_session_human ON codex_session_facts(session) WHERE human<>0;
INSERT INTO maintenance_jobs(name) VALUES('codex_automatic_sessions.v1');
