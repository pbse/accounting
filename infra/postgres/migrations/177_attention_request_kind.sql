BEGIN;
-- A drafted client request waiting for approval is an attention kind of its own ('request'). Saved preferences list the
-- kinds a person chose to count; nobody has chosen about this one yet, so it counts for everyone until they turn it off.
UPDATE attention_preferences SET kinds=array_append(kinds,'request') WHERE NOT 'request'=ANY(kinds);
COMMIT;
