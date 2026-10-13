package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Filesystem validation happens outside app.mu; callers revalidate their scope
// under the lock before choosing a restoration target and persisting anything.
func prepareProject(name, path, icon string) (Project, error) {
	name, ok := cleanLabel(name, 60)
	if !ok {
		return Project{}, errors.New("Project name must contain 1 to 60 characters.")
	}
	folder, err := existingDirectory(path)
	if err != nil {
		return Project{}, err
	}
	if !validIcon(icon) {
		return Project{}, errors.New("Icon must be a PNG data URL, at most 128 KiB and 1024 by 1024 pixels.")
	}
	return Project{ID: newID(), Name: name, Folder: folder, Icon: icon, Folders: []string{}, ArchivedFolders: []string{}}, nil
}

func sameProjectFolder(left, right string) bool {
	return left == right || (runtime.GOOS == "windows" && strings.EqualFold(left, right))
}

// Caller holds app.mu. Restoration keeps identity and visual folders; sessions
// and history remain untouched, as in the existing manual registration flow.
func (a *app) registerProjectLocked(p Project, previous *Project) (Project, error) {
	if previous != nil {
		p.ID, p.Folders, p.ArchivedFolders = previous.ID, previous.Folders, previous.ArchivedFolders
	}
	if err := a.commitLocked(func(d *diskState) {
		if previous := d.project(p.ID); previous != nil {
			*previous = p
		} else {
			d.Projects = append(d.Projects, p)
		}
	}); err != nil {
		return Project{}, err
	}
	return p, nil
}

// Caller holds app.mu across both duplicate detection and registration. Unlike
// manual registration, the general tool never edits an active matching project.
func (a *app) addProjectLocked(p Project) (Project, string, error) {
	var previous *Project
	ids := []string{}
	for i := range a.state.Projects {
		candidate := &a.state.Projects[i]
		if sameProjectFolder(filepath.Clean(candidate.Folder), p.Folder) {
			previous = candidate
			ids = append(ids, candidate.ID)
		}
	}
	if len(ids) > 1 {
		return Project{}, "", fmt.Errorf("Multiple registered projects match this path (project IDs: %s). Resolve the conflicting registrations before adding this path; no project was changed.", strings.Join(ids, ", "))
	}
	if previous != nil && !previous.Removed {
		return *previous, "existing", nil
	}
	outcome := "created"
	if previous != nil {
		outcome = "restored"
	}
	registered, err := a.registerProjectLocked(p, previous)
	if err != nil {
		return Project{}, "", err
	}
	return registered, outcome, nil
}
