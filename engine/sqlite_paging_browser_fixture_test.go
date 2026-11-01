package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSQLitePagingBrowserFixture(t *testing.T) {
	dir, skills := os.Getenv("KLM_SQLITE_BROWSER_FIXTURE"), os.Getenv("KLM_USER_SKILLS_DIR")
	if dir == "" {
		t.Skip("explicit browser fixture preparation only")
	}
	if !developmentBuild || !filepath.IsAbs(dir) || !filepath.IsAbs(skills) || !strings.EqualFold(filepath.Dir(dir), os.TempDir()) || !strings.EqualFold(filepath.Dir(skills), os.TempDir()) {
		t.Fatal("explicit scratch data and skills roots required")
	}
	projectID, sessionID := os.Getenv("KLM_SQLITE_BROWSER_PROJECT"), os.Getenv("KLM_SQLITE_BROWSER_SESSION")
	if projectID == "" || sessionID == "" {
		t.Fatal("explicit scratch IDs required")
	}
	lock, err := lockDataDir(filepath.Join(dir, "engine.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx := context.Background()
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	project, err := store.readProjectMeta(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.ToLower(project.Folder), strings.ToLower(os.TempDir()+string(os.PathSeparator))) {
		t.Fatal("project outside scratch")
	}
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var done bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM store_meta WHERE key='browser_paging_fixture_v1')`).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		var selected Topic
		for i := 0; i < 215; i++ {
			name := fmt.Sprintf("Window topic %03d", i)
			if i == 0 {
				name = "Hidden Καλημέρα target"
			}
			topic, err := st.createTopic(project.ID, name, "blue", "")
			if err != nil {
				return err
			}
			if i == 0 {
				selected = topic
			}
		}
		s, err := st.sessionMeta(sessionID)
		if err != nil {
			return err
		}
		if s.ProjectID != project.ID || s.Status != "idle" {
			return errors.New("choose idle scratch conversation")
		}
		s.TopicID = selected.ID
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		_, err = st.tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('browser_paging_fixture_v1',?)`, selected.ID)
		t.Logf("off-page topic=%s", selected.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("review-skill-%03d", i)
		folder := filepath.Join(skills, id)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		description := "Disposable Library paging fixture"
		if i == 54 {
			description = "Needle skill beyond the first page"
		}
		if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\nname: "+id+"\ndescription: "+description+"\n---\nDo not execute; browser fixture only.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := sqliteSemanticFixture(t).GraphRuns[0].Snapshot
	writes := map[string][]byte{}
	for path, source := range snapshot.Sources {
		writes[path] = []byte(source.Text)
	}
	if err := commitAuthoring(project, writes); err != nil {
		t.Fatal(err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}
