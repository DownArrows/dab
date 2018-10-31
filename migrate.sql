-- Migration from v0.233 to v0.234
ALTER TABLE tracked RENAME TO old_tracked;
DROP INDEX tracked_idx;
DROP VIEW users;

CREATE TABLE IF NOT EXISTS tracked (
	name TEXT PRIMARY KEY,
	created INTEGER NOT NULL,
	not_found BOOLEAN DEFAULT 0 NOT NULL,
	suspended BOOLEAN DEFAULT 0 NOT NULL,
	added INTEGER NOT NULL,
	deleted BOOLEAN DEFAULT 0 NOT NULL,
	hidden BOOLEAN NOT NULL,
	inactive BOOLEAN DEFAULT 0 NOT NULL,
	new BOOLEAN DEFAULT 1 NOT NULL,
	position TEXT DEFAULT "" NOT NULL
) WITHOUT ROWID;

INSERT INTO tracked(name, created, suspended, added, deleted, hidden, inactive, new, position) SELECT name, created, suspended, added, deleted, hidden, inactive, new, position FROM old_tracked;
