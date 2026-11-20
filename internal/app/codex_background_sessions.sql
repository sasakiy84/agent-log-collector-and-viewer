ALTER TABLE session_evidence ADD COLUMN prewarm INTEGER NOT NULL DEFAULT 0;

CREATE INDEX codex_session_prewarm ON session_evidence(session) WHERE prewarm=1;

-- A prewarmed thread can later be used for a conversation.
CREATE TRIGGER session_visibility_conversation AFTER UPDATE OF preview ON sessions
 WHEN EXISTS(SELECT 1 FROM session_evidence p WHERE p.session=NEW.session AND p.prewarm=1) BEGIN
 INSERT INTO work_items(kind,scope,key) VALUES('visibility',NEW.session,'') ON CONFLICT DO NOTHING;
END;

-- Reclassify stored approval prompts whose footer was previously missed.
WITH review_cards AS MATERIALIZED (
 SELECT c.id,c.session FROM session_evidence f INDEXED BY codex_session_human
 CROSS JOIN record_facts c ON c.id=f.event_id
 WHERE f.human<>0 AND c.source='codex' AND json_extract(c.detail,'$.model')='codex-auto-review'
 AND (c.prompt_title GLOB 'The following is the Codex agent history whose request action you are assessing. *'
   OR c.prompt_title GLOB 'The following is the Codex agent history added since your last approval assessment. *')
)
INSERT INTO work_items(kind,scope,key)
 SELECT 'records',id,'' FROM review_cards
 UNION ALL SELECT 'timeline',session,id FROM review_cards WHERE true
 ON CONFLICT DO NOTHING;

-- Probe existing ephemeral sessions for the prewarm span using the session index.
WITH ephemeral_sessions AS MATERIALIZED (
 SELECT DISTINCT session FROM session_evidence INDEXED BY codex_session_metadata
 WHERE (ephemeral<>0 OR persistent<>0 OR temporary<>0) AND ephemeral<>0
)
INSERT INTO work_items(kind,scope,key)
 SELECT 'records',e.id,'' FROM ephemeral_sessions s
 CROSS JOIN records e ON e.session=s.session AND e.origin IN('otel','import')
 WHERE e.source='codex' AND e.signal='traces' AND e.name='startup_prewarm'
 ON CONFLICT DO NOTHING;
