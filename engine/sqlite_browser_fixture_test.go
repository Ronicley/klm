package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in data preparation for the normal CLI/browser probe. Never runs
// in the ordinary suite, never targets default user data, and requires the engine
// lock before opening its already isolated store. This is not model execution.
func TestPrepareSQLiteBrowserFixture(t *testing.T) {
	dir := os.Getenv("KLM_SQLITE_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("explicit browser fixture setup only")
	}
	if !developmentBuild || !filepath.IsAbs(dir) || !strings.EqualFold(filepath.Dir(dir), os.TempDir()) {
		t.Fatal("fixture must be a direct child of the explicit scratch TEMP directory")
	}
	projectID, sessionID := os.Getenv("KLM_SQLITE_BROWSER_PROJECT"), os.Getenv("KLM_SQLITE_BROWSER_SESSION")
	if projectID == "" || sessionID == "" {
		t.Fatal("explicit scratch project/session IDs required")
	}
	lock, err := lockDataDir(filepath.Join(dir, "engine.lock"))
	if err != nil {
		t.Fatal("stop only the owned fixture engine first", err)
	}
	defer lock.Close()
	ctx := context.Background()
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var done bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM store_meta WHERE key='browser_fixture_v1')`).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		p, err := st.project(projectID)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(p.Folder), strings.ToLower(os.TempDir()+string(os.PathSeparator))) {
			return errors.New("project is outside scratch TEMP")
		}
		s, err := st.sessionMeta(sessionID)
		if err != nil {
			return err
		}
		if s.ProjectID != p.ID || s.Status != "idle" {
			return errors.New("choose the idle scratch conversation")
		}
		topic, err := st.createTopic(p.ID, "History validation", "blue", "")
		if err != nil {
			return err
		}
		s.TopicID = topic.ID
		s.Title = "History validation"
		for n := 0; n < 260; n++ {
			e := event("assistant", fmt.Sprintf("History row %03d — durable SQLite content", n))
			e.ID = fmt.Sprintf("browser-history-%03d", n)
			e.Status = "completed"
			e.CreatedAt = time.Date(2026, 1, 1, 12, 0, n, 0, time.UTC).Format(time.RFC3339Nano)
			if _, err := st.appendEvent(s.ID, e); err != nil {
				return err
			}
		}
		e := event("assistant", strings.Repeat("Unicode content 界😀\n", 150000))
		e.ID = "browser-large-event"
		e.Status = "completed"
		if _, err := st.appendEvent(s.ID, e); err != nil {
			return err
		}
		s.UpdatedAt = now()
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		for n := 0; n < 125; n++ {
			item := newTopLevelSession(p.ID, fmt.Sprintf("Paged session %03d", n), "Ungrouped", "pi", "", "")
			if err := st.createSession(item, dir); err != nil {
				return err
			}
		}
		cold := Project{ID: newID(), Name: "Cold project", Folder: filepath.Join(os.TempDir(), "klm-sqlite-cold-project-c9")}
		if info, err := os.Stat(cold.Folder); err != nil || !info.IsDir() {
			return errors.New("create the disposable cold project directory first")
		}
		if err := st.putRoot("projects", cold.ID, cold); err != nil {
			return err
		}
		for n := 0; n < 20; n++ {
			item := newTopLevelSession(cold.ID, fmt.Sprintf("Cold session %03d", n), "Ungrouped", "pi", "", "")
			if err := st.createSession(item, dir); err != nil {
				return err
			}
			if _, err := st.appendEvent(item.ID, event("assistant", "This cold history must remain unopened until selected.")); err != nil {
				return err
			}
		}
		_, err = st.tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('browser_fixture_v1','prepared')`)
		t.Logf("fixture project=%s session=%s coldProject=%s", p.ID, s.ID, cold.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}
