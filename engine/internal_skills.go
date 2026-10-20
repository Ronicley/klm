package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Internal skills are explicitly associated with roles, never directory-discovered.
func internalSkillRoot() (string, error) {
	if dir := os.Getenv("KLM_SKILLS_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("KLM_SKILLS_DIR must be absolute.")
		}
		return dir, nil // Authoritative even when missing/invalid.
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "skills")
		if _, err := os.Stat(dir); err == nil {
			return dir, nil
		}
	}
	if _, source, _, ok := runtime.Caller(0); ok {
		return filepath.Join(filepath.Dir(source), "skills"), nil
	}
	return "", errors.New("Cannot locate internal skills; configure KLM_SKILLS_DIR.")
}

func (a *app) captureInternalSkills(role string) ([]SkillSnapshot, error) {
	result := []SkillSnapshot{}
	if role != sessionRoleGeneralAgent {
		return result, nil
	}
	root, err := internalSkillRoot()
	if err != nil {
		return nil, err
	}
	p, err := stableSkillPackage(root, "skill-library-manager", true)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.materializeSkill(p, true)
	return append(result, snapshot), err
}

// This is role guidance, not a native external-skill allowlist. All general-agent
// harnesses receive the actual captured instructions and engine-resolved root;
// their native skill/install defaults must not select a different destination.
func (a *app) internalSkillGuidance(role string, skills []SkillSnapshot) (string, error) {
	if role != sessionRoleGeneralAgent || len(skills) == 0 {
		return "", nil
	}
	root, err := skillLibraryRoot()
	if err != nil {
		return "", err
	}
	var guidance strings.Builder
	guidance.WriteString("# KLM Agent skill installation\n\nEngine user skill library: " + root + "\n\n")
	for _, skill := range skills {
		if !skill.Internal || skill.ID != "skill-library-manager" {
			return "", errors.New("Unexpected general-agent internal skill.")
		}
		captured, err := a.readSkillSnapshot(skill)
		if err != nil {
			return "", err
		}
		body, format := captured.Files["SKILL.md"], captured.Files["references/installation-format.md"]
		if len(body) == 0 || len(format) == 0 || len(body)+len(format) > 64<<10 {
			return "", errors.New("Internal installation guidance is missing or exceeds 64 KiB.")
		}
		guidance.WriteString("## Internal skill: " + skill.Name + "\n\nCaptured instructions directory: " + a.skillSnapshotPath(skill) + "\n\n")
		guidance.Write(body)
		guidance.WriteString("\n\n## Installation format\n\n")
		guidance.Write(format)
	}
	return guidance.String(), nil
}
