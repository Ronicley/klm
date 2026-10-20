package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func internalSkillFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	format := filepath.Join(root, "skill-library-manager", "references", "installation-format.md")
	if err := os.MkdirAll(filepath.Dir(format), 0700); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: skill-library-manager\ndescription: Fixture installation guidance\n---\nfixture-manager-instructions\n"
	if err := os.WriteFile(filepath.Join(root, "skill-library-manager", "SKILL.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(format, []byte("fixture-installation-format-v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "user-skills")
	t.Setenv("KLM_SKILLS_DIR", root)
	t.Setenv("KLM_USER_SKILLS_DIR", library)
	return library, format
}

func internalSkillAdapter(t *testing.T, harness string) *adapter {
	t.Helper()
	s := Session{ID: "general", Role: sessionRoleGeneralAgent, ExecutionCWD: t.TempDir(), Harness: harness, Status: "running", Events: []Event{}, CreatedAt: now(), UpdatedAt: now()}
	a := &app{dir: t.TempDir(), state: diskState{Version: 2, Projects: []Project{}, Sessions: []Session{s}, Native: map[string]nativeSession{}}}
	p := &adapter{app: a, id: s.ID, role: s.Role, harness: s.Harness, turn: &turn{ctx: context.Background()}}
	if err := p.prepareCapabilities(s); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInternalSkillInstallationGuidanceAllHarnesses(t *testing.T) {
	library, _ := internalSkillFixture(t)
	for _, harness := range []string{"pi", "opencode", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := internalSkillAdapter(t, harness)
			defer p.closeMCP()
			for _, required := range []string{"Engine user skill library: " + library, "fixture-manager-instructions", "fixture-installation-format-v1"} {
				if !strings.Contains(p.internalSkillPrompt, required) {
					t.Fatalf("%s did not receive %q", harness, required)
				}
			}
			if len(p.internalSkills) != 1 || !p.internalSkills[0].Internal {
				t.Fatal("mandatory internal skill was not captured")
			}
			if managedLibraryHarness(harness) {
				if p.capabilities == nil || len(p.capabilities.Skills) != 1 || p.capabilities.Skills[0].ID != "skill-library-manager" {
					t.Fatal("managed harness lost its captured manager snapshot")
				}
			} else if p.capabilities != nil {
				t.Fatal("role instructions were presented as an exclusive external Library snapshot")
			}
			origin := *p.app.state.session(p.id)
			origin.Capabilities.Applied = p.capabilities
			inherited := inheritedCapabilities(&origin)
			if !selectionEmpty(inherited.Desired) {
				t.Fatal("internal manager leaked into inherited external selection")
			}
		})
	}
}

func TestInternalSkillInstallationSnapshotAndRoleScope(t *testing.T) {
	_, format := internalSkillFixture(t)
	p := internalSkillAdapter(t, "opencode")
	defer p.closeMCP()
	key := p.runtimeToolKey()
	if err := os.WriteFile(format, []byte("fixture-installation-format-v2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.internalSkillPrompt, "fixture-installation-format-v2") {
		t.Fatal("active turn followed an internal source edit")
	}
	next := &adapter{app: p.app, id: p.id, role: p.role, harness: p.harness, turn: &turn{ctx: context.Background()}}
	if err := next.prepareCapabilities(*p.app.state.session(p.id)); err != nil {
		t.Fatal(err)
	}
	defer next.closeMCP()
	if !strings.Contains(next.internalSkillPrompt, "fixture-installation-format-v2") || key == next.runtimeToolKey() {
		t.Fatal("next turn did not capture the edit in runtime identity")
	}
	for _, role := range []string{"", sessionRoleSideAgent, sessionRoleSubagent, "graph_node"} {
		skills, err := p.app.captureInternalSkills(role)
		if err != nil || len(skills) != 0 {
			t.Fatalf("internal skill distributed to role %q: %v", role, err)
		}
		guidance, err := p.app.internalSkillGuidance(role, p.internalSkills)
		if err != nil || guidance != "" {
			t.Fatalf("internal instructions distributed to role %q: %v", role, err)
		}
	}
	t.Setenv("KLM_SKILLS_DIR", filepath.Join(t.TempDir(), "missing-override"))
	if _, err := p.app.captureInternalSkills(sessionRoleGeneralAgent); err == nil {
		t.Fatal("invalid explicit internal override silently fell back")
	}
}
