package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run with -trimpath, outside the checkout, without sibling skills. The cwd
// reproduces the installed /opt/klm/bin layout: klm is a file, not a directory.
func TestInternalSkillBinaryDistribution(t *testing.T) {
	t.Setenv("KLM_SKILLS_DIR", "")
	library := filepath.Join(t.TempDir(), "absent-user-library")
	t.Setenv("KLM_USER_SKILLS_DIR", library)
	t.Chdir(t.TempDir())
	if err := os.WriteFile("klm", []byte("executable placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{dir: t.TempDir()}
	staging := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, staging)
	}
	skills, err := a.captureInternalSkills(sessionRoleGeneralAgent)
	if err != nil || len(skills) != 1 {
		t.Fatalf("capture: %v (%d skills)", err, len(skills))
	}
	snapshot := skills[0]
	if snapshot.ID != "skill-library-manager" || !snapshot.Internal {
		t.Fatalf("wrong role asset identity: %+v", snapshot)
	}
	captured, err := a.readSkillSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{}
	prefix := "skills/skill-library-manager/"
	err = fs.WalkDir(packagedInternalSkills, strings.TrimSuffix(prefix, "/"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		body, err := packagedInternalSkills.ReadFile(path)
		if err != nil {
			return err
		}
		expected[strings.TrimPrefix(path, prefix)] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) < 2 || len(captured.Files) != len(expected) {
		t.Fatal("incomplete internal package")
	}
	for name, body := range expected {
		t.Logf("%s SHA-256 %x", name, sha256.Sum256(body))
		if !bytes.Equal(captured.Files[name], body) {
			t.Fatalf("changed or missing captured asset: %s", name)
		}
	}
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	if snapshot.Revision != hex.EncodeToString(hash[:]) {
		t.Fatal("snapshot revision does not match the full package SHA-256")
	}
	t.Logf("Full package revision %s", snapshot.Revision)
	guidance, err := a.internalSkillGuidance(sessionRoleGeneralAgent, skills)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "references/installation-format.md"} {
		if len(expected[name]) == 0 || !strings.Contains(guidance, string(expected[name])) {
			t.Fatalf("mandatory guidance missing: %s", name)
		}
	}
	if !strings.Contains(guidance, a.skillSnapshotPath(snapshot)) {
		t.Fatal("guidance did not use the persistent captured path")
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary embedded source was not removed: %v", err)
	}
	if _, err := os.Stat(library); !os.IsNotExist(err) {
		t.Fatalf("capture touched the user skill library: %v", err)
	}
	// Reuse must validate existing snapshots, never silently repair changed bytes.
	if err := os.WriteFile(filepath.Join(a.skillSnapshotPath(snapshot), "SKILL.md"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.captureInternalSkills(sessionRoleGeneralAgent); err == nil {
		t.Fatal("corrupted existing snapshot was accepted or overwritten")
	}
}

func TestInternalSkillAssetOverrideAndLifetime(t *testing.T) {
	_, format := internalSkillFixture(t)
	a := &app{dir: t.TempDir()}
	first, err := a.captureInternalSkills(sessionRoleGeneralAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(format, []byte("edited-installation-format\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := a.captureInternalSkills(sessionRoleGeneralAgent)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Revision == second[0].Revision {
		t.Fatal("next capture ignored authoritative source edit")
	}
	if err := os.RemoveAll(os.Getenv("KLM_SKILLS_DIR")); err != nil {
		t.Fatal(err)
	}
	for i, skills := range [][]SkillSnapshot{first, second} {
		guidance, err := a.internalSkillGuidance(sessionRoleGeneralAgent, skills)
		if err != nil {
			t.Fatal("snapshot depended on removed source: ", err)
		}
		want := "fixture-installation-format-v1"
		if i == 1 {
			want = "edited-installation-format"
		}
		if !strings.Contains(guidance, want) {
			t.Fatal("captured turn guidance changed")
		}
	}
	if _, err := a.captureInternalSkills(sessionRoleGeneralAgent); err == nil {
		t.Fatal("removed authoritative source silently fell back")
	}
}

func TestInternalSkillAssetInvalidOverrideAndRoleScope(t *testing.T) {
	a := &app{dir: t.TempDir()}
	missing := filepath.Join(t.TempDir(), "missing")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"relative-skills", missing, file, t.TempDir()} {
		t.Setenv("KLM_SKILLS_DIR", root)
		if _, err := a.captureInternalSkills(sessionRoleGeneralAgent); err == nil {
			t.Fatalf("invalid authoritative source fell back: %s", root)
		}
		for _, role := range []string{"", sessionRoleSideAgent, sessionRoleSubagent, "graph_node"} {
			skills, err := a.captureInternalSkills(role)
			if err != nil || len(skills) != 0 {
				t.Fatalf("role %q received or tried to load internal assets: %v", role, err)
			}
			guidance, err := a.internalSkillGuidance(role, []SkillSnapshot{{ID: "skill-library-manager", Internal: true}})
			if err != nil || guidance != "" {
				t.Fatalf("role %q received internal guidance: %v", role, err)
			}
		}
	}
}
