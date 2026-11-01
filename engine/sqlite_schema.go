package main

// Runtime tables are deliberately independent of the import-only positional
// tree. No whole diskState, project, session history or domain ledger JSON row.
const sqliteSchema = `
PRAGMA application_id=1263291697;
PRAGMA user_version=4;
CREATE TABLE store_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
CREATE TABLE permission_reply_intents(request_id TEXT PRIMARY KEY,session_id TEXT NOT NULL,grant_id TEXT NOT NULL) STRICT;
CREATE INDEX permission_reply_intents_grant ON permission_reply_intents(grant_id);
CREATE TABLE runtime_activity(session_id TEXT PRIMARY KEY,active INTEGER NOT NULL DEFAULT 0,reasoning_title TEXT NOT NULL DEFAULT '',boundary INTEGER NOT NULL DEFAULT 0) STRICT;
CREATE INDEX runtime_activity_active ON runtime_activity(active,session_id);
CREATE INDEX runtime_activity_reasoning ON runtime_activity(session_id) WHERE reasoning_title<>'';
CREATE TABLE paired_clients(
 id TEXT PRIMARY KEY, name TEXT NOT NULL, credential_hash TEXT NOT NULL,
 created_at TEXT NOT NULL, last_used_at TEXT NOT NULL
) STRICT;
CREATE TABLE engine_access_settings(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), initialized INTEGER NOT NULL CHECK(initialized IN (0,1))
) STRICT;
CREATE TABLE engine_access_values(
 kind TEXT NOT NULL CHECK(kind IN ('origin','proxy')), position INTEGER NOT NULL CHECK(position>=0), value TEXT NOT NULL,
 PRIMARY KEY(kind,position)
) STRICT;
`
