-- Navigation counts should read compact metadata, not every log or preview.
CREATE INDEX catalog_roots_recent ON session_catalog(end DESC,session,source)
 WHERE internal=0 AND length(parent)=0;
CREATE TABLE processing_buckets(
 session TEXT NOT NULL,origin TEXT NOT NULL,minute TEXT NOT NULL,
 count INTEGER NOT NULL,activity INTEGER NOT NULL,errors INTEGER NOT NULL,
 PRIMARY KEY(session,origin,minute)) WITHOUT ROWID;
INSERT INTO maintenance_jobs(name) VALUES('processing.cards.v1'),('processing.timeline.v1');

-- While history is rebuilt, only rows behind its committed cursor are live.
-- Each batch's counters and cursor commit together; late inserts, updates and
-- deletes therefore cannot be missed or counted twice during the rebuild.

CREATE TRIGGER processing_cards_insert AFTER INSERT ON event_cards
 WHEN NEW.conversation=0 AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
CREATE TRIGGER processing_cards_delete AFTER DELETE ON event_cards
 WHEN OLD.conversation=0 AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_cards_update_remove AFTER UPDATE ON event_cards
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_cards_update_add AFTER UPDATE ON event_cards
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;

CREATE TRIGGER processing_timeline_insert AFTER INSERT ON timeline_entries
 WHEN NEW.conversation=0 AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
CREATE TRIGGER processing_timeline_delete AFTER DELETE ON timeline_entries
 WHEN OLD.conversation=0 AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_timeline_update_remove AFTER UPDATE ON timeline_entries
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_timeline_update_add AFTER UPDATE ON timeline_entries
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
