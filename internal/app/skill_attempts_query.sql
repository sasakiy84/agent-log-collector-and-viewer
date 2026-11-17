, skill_events AS NOT MATERIALIZED (
 SELECT k.* FROM scoped_skill_events k WHERE NOT EXISTS(
 SELECT 1 FROM skill_attempt_links wrapper
 JOIN skill_attempt_links child ON child.session=wrapper.session
 AND child.source=wrapper.source AND child.owner=wrapper.owner
 AND child.chunk=wrapper.chunk AND child.wrapper=0
 JOIN scoped_skill_events concrete ON concrete.event_id=child.event_id
 AND concrete.name=k.name AND concrete.plugin=k.plugin
 WHERE wrapper.event_id=k.event_id AND wrapper.wrapper=1
 GROUP BY wrapper.chunk HAVING COUNT(DISTINCT concrete.group_key)=1)
 -- Copilot can report an invocation on the parent span and the skill tool on
 -- a direct child. Require a unique report and tool; never pair by timestamp.
 AND NOT (k.evidence='reported' AND EXISTS(
 SELECT 1 FROM record_facts parent JOIN record_facts child
 ON child.session=parent.session AND child.source=parent.source
 AND child.trace=parent.trace AND child.parent_span=parent.span
 JOIN scoped_skill_events concrete ON concrete.event_id=child.id
 AND concrete.name=k.name AND concrete.plugin=k.plugin
 AND concrete.evidence IN ('attempt','read','pending','failed')
 WHERE parent.id=k.event_id AND parent.source='copilot'
 AND parent.trace<>'' AND parent.span<>''
 AND (SELECT COUNT(DISTINCT report.group_key) FROM scoped_skill_events report
 WHERE report.event_id=k.event_id AND report.name=k.name
 AND report.plugin=k.plugin AND report.evidence='reported')=1
 GROUP BY parent.id HAVING COUNT(DISTINCT concrete.group_key)=1)))
