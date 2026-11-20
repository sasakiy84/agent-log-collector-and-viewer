-- Drop historical selections after their jobs no longer need a snapshot. A
-- session ID may have been reimported or selected again since the earlier job.
DELETE FROM session_versions
WHERE session IN (
 SELECT s.session FROM archive_sessions s JOIN archive_jobs j ON j.id=s.archive_id
 WHERE j.status IN ('compacting','deleted','failed','canceled')
)
AND session NOT IN (
 SELECT s.session FROM archive_sessions s JOIN archive_jobs j ON j.id=s.archive_id
 WHERE j.status IN ('preparing','ready','deleting')
)
AND NOT EXISTS (SELECT 1 FROM records WHERE session=session_versions.session);

DELETE FROM archive_sessions WHERE archive_id IN (
 SELECT id FROM archive_jobs WHERE status IN ('compacting','deleted','failed','canceled')
);
