-- Collapsed processing groups need only these flags and chronological keys.
-- Extend the existing paging indexes instead of adding another full-history
-- index; large detail payloads stay out of count/summary reads.
DROP INDEX timeline_page;
CREATE INDEX timeline_page ON timeline_entries(session,time,sequence,id,conversation,activity,is_error);
DROP INDEX cards_page;
CREATE INDEX cards_page ON event_cards(session,origin,time,sequence,id,conversation,activity,is_error);
