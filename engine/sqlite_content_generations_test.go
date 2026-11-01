package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestSQLiteContentGenerationPublication(t *testing.T) {
	ctx := context.Background()
	store, path := sqliteSessionStore(t, 0)
	source := func() io.Reader {
		return io.MultiReader(strings.NewReader(`{"id":"large","type":"assistant","status":"completed","text":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`\n\u00e9\ud83d\udc31"}`))
	}
	generation, err := store.stageEventGeneration(ctx, "general", "large", source())
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("unpublished generation became visible", err)
	}
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.publishEventGeneration("general", Event{ID: "large"}, generation)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err = store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 1 || page.Events[0].Content == nil || page.Events[0].Content.Digest != generation.Digest {
		t.Fatal("large content contract not published", err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if err := reopened.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	err = reopened.readSnapshot(ctx, func(tx *sql.Tx) error {
		key := sqliteOwnedKey("general", "0")
		text, more, err := sqliteEventTextWindow(ctx, tx, key, 25<<20, 8)
		if err != nil {
			return err
		}
		if text != "\né🐱" || more {
			t.Fatalf("bounded Unicode tail: %q %v", text, more)
		}
		h := sha256.New()
		if err := writeSQLiteRecordContent(ctx, tx, h, "session_events", key); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != generation.Digest {
			t.Fatal("canonical content changed after restart/export")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteGenerationCleanupOwnership(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	g, err := store.stageEventGeneration(ctx, "general", "staging", strings.NewReader(`{"id":"staging","type":"assistant","text":"complete"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.collectEventGenerations(ctx); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := store.readers.QueryRow(`SELECT EXISTS(SELECT 1 FROM event_content_generations WHERE id=?)`, g.ID).Scan(&exists); err != nil || !exists {
		t.Fatal("collector reclaimed an active stage", err)
	}
	store.setContentLive(g.ID, false)
	if err := store.collectEventGenerations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.readers.QueryRow(`SELECT EXISTS(SELECT 1 FROM event_content_generations WHERE id=?)`, g.ID).Scan(&exists); err != nil || exists {
		t.Fatal("abandoned stage retained", err)
	}
}

func TestSQLiteContentGenerationMigration(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	for _, table := range []string{"delegation_calls", "delegation_invocations", "delegation_children", "delegation_batches", "agent_bindings", "agent_catalog_changes", "agent_sources", "global_agents"} {
		if _, err := store.writer.Exec("DROP TABLE data_" + table + "_chunks; DROP TABLE data_" + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.writer.Exec(`DROP TABLE event_content_appends; DROP TABLE event_content_publications; DROP TABLE event_content_generation_chunks; DROP TABLE event_content_generations; PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	// Version-1 authority verification can still read old chunks before migration.
	if _, err := store.readHistory(ctx, "a", "", "", 50); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateContentGenerations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateContentGenerations(ctx); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	if _, err := store.readHistory(ctx, "a", "", "", 50); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteContentGenerationAppendAndFavorite(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	if _, err := store.writer.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	dir := store.dir
	if err := prepareProtocolStaging(dir); err != nil {
		t.Fatal(err)
	}
	scope := &protocolContentScope{dir: dir, ctx: ctx}
	defer scope.Close()
	p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: "pi", keys: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
	if err := p.put("text", "assistant", "", strings.Repeat("x", 1<<20), "running", false, nil); err != nil {
		t.Fatal(err)
	}
	var initialChunks int
	if err := store.readers.QueryRow(`SELECT count(*) FROM event_content_generation_chunks`).Scan(&initialChunks); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := p.put("text", "assistant", "", "é🐱", "running", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	var afterChunks int
	if err := store.readers.QueryRow(`SELECT count(*) FROM event_content_generation_chunks`).Scan(&afterChunks); err != nil || afterChunks != initialChunks {
		t.Fatalf("append copied old chunks: before=%d after=%d err=%v", initialChunks, afterChunks, err)
	}
	key := sqliteOwnedKey("general", "0")
	err := store.readSnapshot(ctx, func(tx *sql.Tx) error {
		text, more, err := sqliteEventTextWindow(ctx, tx, key, 1<<20, 16)
		if err != nil {
			return err
		}
		if text != strings.Repeat("é🐱", 5) || more {
			t.Fatalf("virtual append tail: %q %v", text, more)
		}
		h := sha256.New()
		if err := writeSQLiteRecordContent(ctx, tx, h, "session_events", key); err != nil {
			return err
		}
		var digest string
		if err := tx.QueryRow(`SELECT digest FROM data_session_events WHERE id=?`, key).Scan(&digest); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			t.Fatal("append digest mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.prepareEventMutation(ctx, "general", key, func(e *Event) error { e.Status = "completed"; e.Favorite = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.publishEventMutation(prepared) }); err != nil {
		t.Fatal(err)
	}
	err = store.readSnapshot(ctx, func(tx *sql.Tx) error {
		e, err := readSQLiteEventMetadata(ctx, tx, key)
		if err == nil && (!e.Favorite || e.Status != "completed") {
			t.Fatalf("metadata mutation lost: %+v", e)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
