package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxSkillBytes = 16 << 20
const maxSkillFiles = 512

var skillIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type SkillLibraryEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Revision    string `json:"revision,omitempty"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
	Source      string `json:"source"`
}

type SkillSnapshot struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Revision    string `json:"revision"`
	Internal    bool   `json:"internal,omitempty"`
}

type skillPackage struct {
	Entry SkillLibraryEntry
	Files map[string][]byte
}

func skillLibraryRoot() (string, error) {
	if override := os.Getenv("KLM_USER_SKILLS_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("KLM_USER_SKILLS_DIR must be absolute.")
		}
		return filepath.Clean(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".klm", "skills"), nil
}

func validSkillID(id string) bool {
	return len(id) > 0 && len(id) <= 64 && skillIDPattern.MatchString(id) && validAuthoringID(id) && id != "skill-library-manager" && !strings.HasPrefix(id, "klm-internal-")
}

// Reject links, including Windows junctions. The root itself is an explicitly
// configured source; descendants must be ordinary directories/files.
func skillPlainPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !piSamePath(resolved, path) {
		return errors.New("Linked skill files and directories are not supported.")
	}
	return nil
}

func readSkillPackage(root, id string, internal bool) (skillPackage, error) {
	p := skillPackage{Entry: SkillLibraryEntry{ID: id, Source: "user"}, Files: map[string][]byte{}}
	if internal {
		p.Entry.Source = "internal"
	}
	if !internal && !validSkillID(id) {
		return p, errors.New("Use a nonreserved lowercase skill slug (at most 64 characters).")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return p, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return p, err
	}
	dir := filepath.Join(root, id)
	total := 0
	folded := map[string]bool{}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := skillPlainPath(path); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.Count(rel, "/") > 16 {
			return errors.New("Skill exceeds 16 directory levels.")
		}
		if rel != "." {
			key := strings.ToLower(rel)
			if folded[key] || strings.ContainsAny(rel, ":\\") {
				return errors.New("Skill contains conflicting or nonportable paths.")
			}
			folded[key] = true
			for _, part := range strings.Split(rel, "/") {
				stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
				reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || regexp.MustCompile(`^(COM|LPT)[0-9]$`).MatchString(stem)
				if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>:"|?*`) || reserved {
					return errors.New("Skill contains a reserved or invalid filename.")
				}
			}
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("Skill contains a nonregular file.")
		}
		if len(p.Files) >= maxSkillFiles || info.Size() > maxSkillBytes-int64(total) {
			return errors.New("Skill exceeds 512 files or 16 MiB.")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxSkillBytes-total)+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		total += len(data)
		if total > maxSkillBytes {
			return errors.New("Skill exceeds 16 MiB.")
		}
		p.Files[rel] = data
		return nil
	})
	if err != nil {
		return p, err
	}
	encoded, _ := json.Marshal(p.Files)
	p.Entry.Revision = revision(encoded)
	body := p.Files["SKILL.md"]
	if !utf8.Valid(body) || len(body) > 256<<10 {
		return p, errors.New("SKILL.md must be UTF-8 and at most 256 KiB.")
	}
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if !strings.HasPrefix(text, "---\n") {
		return p, errors.New("SKILL.md requires YAML frontmatter.")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return p, errors.New("SKILL.md has unterminated frontmatter.")
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &metadata); err != nil {
		return p, errors.New("Invalid skill frontmatter.")
	}
	if metadata.Name != id || strings.TrimSpace(metadata.Description) == "" || len(metadata.Description) > 1024 {
		return p, errors.New("Skill name must match its directory; description is required (at most 1024 bytes).")
	}
	p.Entry.Name, p.Entry.Description = metadata.Name, strings.TrimSpace(metadata.Description)
	p.Entry.Valid = true
	return p, nil
}

func stableSkillPackage(root, id string, internal bool) (skillPackage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		first, err := readSkillPackage(root, id, internal)
		if err != nil && first.Entry.Revision == "" {
			return first, err
		}
		second, err := readSkillPackage(root, id, internal)
		if err != nil && second.Entry.Revision == "" {
			return second, err
		}
		if first.Entry.Revision == second.Entry.Revision {
			return second, err
		}
	}
	return skillPackage{}, errors.New("Skill changed during capture; retry with stable files.")
}

func scanSkillLibrary() (string, []SkillLibraryEntry, error) {
	root, err := skillLibraryRoot()
	entries := []SkillLibraryEntry{}
	if err != nil {
		return root, entries, err
	}
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return root, entries, nil
	}
	if err != nil {
		return root, entries, err
	}
	seen := map[string]int{}
	for _, dir := range dirs {
		if !dir.IsDir() && dir.Type()&os.ModeSymlink == 0 {
			continue
		}
		id := dir.Name()
		p, readErr := stableSkillPackage(root, id, false)
		entry := p.Entry
		if readErr != nil {
			entry.ID, entry.Source, entry.Valid, entry.Error = id, "user", false, readErr.Error()
		}
		key := strings.ToLower(id)
		if index, exists := seen[key]; exists {
			entries[index].Valid, entries[index].Error = false, "Case-insensitive skill ID collision."
			entry.Valid, entry.Error = false, "Case-insensitive skill ID collision."
		}
		seen[key] = len(entries)
		entries = append(entries, entry)
		if len(entries) >= 1000 {
			return root, entries, errors.New("Skill library exceeds 1000 entries.")
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return root, entries, nil
}

func (a *app) materializeSkill(p skillPackage, internal bool) (SkillSnapshot, error) {
	snapshot := SkillSnapshot{ID: p.Entry.ID, Name: p.Entry.Name, Description: p.Entry.Description, Revision: p.Entry.Revision, Internal: internal}
	parent := filepath.Join(a.dir, "capabilities", "skills")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return snapshot, err
	}
	path := filepath.Join(parent, snapshot.Revision)
	if _, err := os.Stat(path); err == nil {
		return snapshot, a.validateSkillSnapshot(snapshot)
	}
	temp, err := os.MkdirTemp(parent, ".capture-")
	if err != nil {
		return snapshot, err
	}
	defer os.RemoveAll(temp)
	for name, data := range p.Files {
		destination := filepath.Join(temp, snapshot.ID, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return snapshot, err
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			return snapshot, err
		}
	}
	if err := os.Rename(temp, path); err != nil {
		if _, existing := os.Stat(path); existing != nil {
			return snapshot, err
		}
	}
	return snapshot, a.validateSkillSnapshot(snapshot)
}

func (a *app) skillSnapshotPath(s SkillSnapshot) string {
	return filepath.Join(a.dir, "capabilities", "skills", s.Revision, s.ID)
}

func (a *app) validateSkillSnapshot(s SkillSnapshot) error {
	_, err := a.readSkillSnapshot(s)
	return err
}

func (a *app) readSkillSnapshot(s SkillSnapshot) (skillPackage, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(s.Revision) {
		return skillPackage{}, errors.New("Invalid skill snapshot revision.")
	}
	if !validSkillID(s.ID) && !(s.Internal && s.ID == "skill-library-manager") {
		return skillPackage{}, errors.New("Invalid skill snapshot identity.")
	}
	p, err := readSkillPackage(filepath.Join(a.dir, "capabilities", "skills", s.Revision), s.ID, s.Internal)
	if err != nil {
		return skillPackage{}, err
	}
	encoded, _ := json.Marshal(p.Files)
	if revision(encoded) != s.Revision {
		return skillPackage{}, fmt.Errorf("Skill snapshot %s is corrupted.", s.ID)
	}
	if p.Entry.Name != s.Name || p.Entry.Description != s.Description {
		return skillPackage{}, errors.New("Skill snapshot metadata mismatch.")
	}
	return p, nil
}
