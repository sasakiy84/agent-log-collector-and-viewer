ALTER TABLE session_catalog ADD COLUMN project_key TEXT GENERATED ALWAYS AS (
 CASE WHEN json_type(preview,'$.project')='text' AND json_extract(preview,'$.project')<>''
 THEN 'name:'||json_extract(preview,'$.project')
 WHEN json_type(preview,'$.cwd')='text' AND json_extract(preview,'$.cwd')<>''
 THEN 'path:'||json_extract(preview,'$.cwd') ELSE '' END) VIRTUAL;
CREATE INDEX catalog_project_recent ON session_catalog(project_key,end DESC,session);
ALTER TABLE folder_members ADD COLUMN project TEXT;
CREATE INDEX project_members_recent ON folder_members(project,source,diagnostic,end DESC,session);
CREATE TABLE project_totals(
 project TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL,PRIMARY KEY(project,source,diagnostic)) WITHOUT ROWID;
INSERT INTO folder_dirty SELECT session FROM session_catalog WHERE true ON CONFLICT DO NOTHING;
