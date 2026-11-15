-- Sidebar counts and recency must describe the root sessions shown in the
-- normal list. Preserve complete totals for the explicitly inclusive export UI.
ALTER TABLE folder_members ADD COLUMN child INTEGER NOT NULL DEFAULT 0;
UPDATE folder_members SET child=COALESCE((SELECT length(s.parent)>0 FROM session_catalog s WHERE s.session=folder_members.session),1);
CREATE INDEX project_members_roots_recent ON folder_members(project,source,diagnostic,end DESC,session) WHERE child=0;
ALTER TABLE project_totals ADD COLUMN root_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE project_totals ADD COLUMN root_end TEXT NOT NULL DEFAULT '';
DELETE FROM project_totals;
INSERT INTO project_totals(project,source,diagnostic,count,end,root_count,root_end)
 SELECT project,source,diagnostic,COUNT(*),MAX(end),SUM(child=0),COALESCE(MAX(CASE WHEN child=0 THEN end END),'')
 FROM folder_members WHERE project IS NOT NULL GROUP BY project,source,diagnostic;
