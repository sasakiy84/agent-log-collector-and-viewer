-- Keep title eligibility separate from the user role: injected context remains
-- visible in the timeline and original JSON, but is not a session title.
ALTER TABLE event_cards ADD COLUMN prompt_title TEXT NOT NULL DEFAULT '';
CREATE INDEX cards_prompt_title ON event_cards(session,time,sequence,id) WHERE prompt_title<>'';
CREATE INDEX cards_origin_prompt_title ON event_cards(session,origin,time,sequence,id) WHERE prompt_title<>'';
CREATE INDEX cards_title_backfill ON event_cards(id) WHERE is_user=1;
INSERT INTO maintenance_jobs(name) VALUES('titles.v1');
