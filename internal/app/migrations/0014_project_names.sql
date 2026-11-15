-- Project identity is the displayed name, independent of working directory.
-- Only cached session/folder metadata is touched; original events stay intact.
DROP INDEX catalog_project_recent;
ALTER TABLE session_catalog DROP COLUMN project_key;
ALTER TABLE session_catalog ADD COLUMN project_directory TEXT GENERATED ALWAYS AS (
 CASE WHEN json_type(preview,'$.cwd')='text'
 THEN replace(rtrim(json_extract(preview,'$.cwd'),'/\'),'\','/') ELSE '' END) VIRTUAL;
-- Trimming with all non-separator characters stops at the final slash;
-- its remaining prefix length locates the basename without a custom function.
ALTER TABLE session_catalog ADD COLUMN project_key TEXT GENERATED ALWAYS AS (
 CASE WHEN json_type(preview,'$.project')='text' AND json_extract(preview,'$.project')<>''
 THEN 'name:'||json_extract(preview,'$.project')
 WHEN json_type(preview,'$.cwd')='text' AND json_extract(preview,'$.cwd')<>''
 THEN 'name:'||COALESCE(NULLIF(substr(project_directory,
 length(rtrim(project_directory,replace(project_directory,'/','')))+1),''),json_extract(preview,'$.cwd'))
 ELSE '' END) VIRTUAL;
CREATE INDEX catalog_project_recent ON session_catalog(project_key,end DESC,session);

-- Publish membership and counts together, preserving source/diagnostic totals
-- and the latest activity across every directory sharing a project name.
UPDATE folder_members SET project=(SELECT s.project_key FROM session_catalog s WHERE s.session=folder_members.session);
DELETE FROM project_totals;
INSERT INTO project_totals(project,source,diagnostic,count,end)
 SELECT project,source,diagnostic,COUNT(*),MAX(end) FROM folder_members
 WHERE project IS NOT NULL GROUP BY project,source,diagnostic;
