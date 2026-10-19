package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
