package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Embed the whole approved internal package, not only the main instructions.
// This is not discovery or installation into the user's external skill Library.
//
//go:embed all:skills/skill-library-manager
var packagedInternalSkills embed.FS

func packagedInternalSkill() (skillPackage, error) {
	// Reuse the existing complete-package validation and revision algorithm on a
	// private temporary source. Only the content-addressed snapshot survives
	// capture; no mutable source cache or temporary path is given to a harness.
	root, err := os.MkdirTemp("", "klm-internal-skills-")
	if err != nil {
		return skillPackage{}, err
	}
	defer os.RemoveAll(root)
	err = fs.WalkDir(packagedInternalSkills, "skills", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "skills" {
			return nil
		}
		destination := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "skills/")))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		body, err := packagedInternalSkills.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, body, 0600)
	})
	if err != nil {
		return skillPackage{}, err
	}
	return stableSkillPackage(root, "skill-library-manager", true)
}
