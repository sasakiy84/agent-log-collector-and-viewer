-- A separate resumable job also repairs databases that finished conversation.v1.
-- HTTP serving starts before this bounded repair; original OTLP is preserved.
INSERT INTO maintenance_jobs(name) VALUES('codex_tool_timing.v1');
