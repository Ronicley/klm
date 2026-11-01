package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type directoryMutation struct {
	Parent, Name, NewName, Confirmation string
}

func readDirectoryMutation(w http.ResponseWriter, r *http.Request) (directoryMutation, string, bool) {
	var body directoryMutation
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if !decode(w, r, &body) {
		return body, "", false
	}
	parent, valid := browserDirectoryPath(body.Parent)
	if !valid || !validNewDirectoryName(body.Name) {
		fail(w, 400, "Choose a valid folder in an absolute parent directory.")
		return body, "", false
	}
	body.Parent = parent
	child := filepath.Join(parent, body.Name)
	if len(child) > directoryPathBytes || filepath.Dir(child) != parent {
		fail(w, 400, "The folder path is invalid or too long.")
		return body, "", false
	}
	return body, child, true
}

func directoryContainsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// Resolve existing ancestors as well: a temporarily missing registered folder
// still protects its containing directory, including through directory aliases.
func resolveProtectedDirectory(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current, suffix := path, ""
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		parent := filepath.Dir(current)
		if !errors.Is(err, os.ErrNotExist) || parent == current {
			return "", err
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
		current = parent
	}
}

// Caller holds app.mu through validation and the single OS operation. Project
// registration uses the same lock; a registered path cannot slip into the move.
// These are integrity guards, not a filesystem sandbox against external writers.
func (a *app) allowDirectoryMutationLocked(w http.ResponseWriter, r *http.Request, source, destination string) bool {
	if a.closing || a.storageErr != nil || r.Context().Err() != nil {
		fail(w, 503, "Engine is unavailable.")
		return false
	}
	info, err := os.Lstat(source)
	if err != nil {
		directoryBrowserError(w, err)
		return false
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		fail(w, 400, "Choose a directory, not a file or directory link.")
		return false
	}
	paths := []string{source}
	if destination != "" {
		paths = append(paths, destination)
	}
	for i, path := range paths {
		resolved, err := resolveProtectedDirectory(path)
		if err != nil {
			fail(w, 403, "Could not resolve this directory safely. Check its permissions.")
			return false
		}
		paths[i] = resolved
		volumeRoot := filepath.Clean(filepath.VolumeName(resolved) + string(filepath.Separator))
		if resolved == volumeRoot || filepath.Dir(resolved) == resolved {
			fail(w, 409, "Filesystem roots cannot be renamed or deleted.")
			return false
		}
	}
	type protectedDirectory struct {
		path string
		tree bool
	}
	protected := []protectedDirectory{{a.dir, true}}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		protected = append(protected, protectedDirectory{home, false})
	} else {
		fail(w, 503, "Could not check the engine user's home directory.")
		return false
	}
	if a.store != nil {
		rows, err := a.store.readers.QueryContext(r.Context(), `SELECT x_folder FROM data_projects`)
		if err != nil {
			fail(w, 503, "Could not check registered project directories.")
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var folder string
			if err := rows.Scan(&folder); err != nil {
				fail(w, 503, "Could not check registered project directories.")
				return false
			}
			protected = append(protected, protectedDirectory{folder, false})
		}
		if rows.Err() != nil {
			fail(w, 503, "Could not check registered project directories.")
			return false
		}
	} else {
		for _, project := range a.state.Projects {
			protected = append(protected, protectedDirectory{project.Folder, false})
		}
	}
	for _, item := range protected {
		if item.path == "" {
			continue
		}
		resolved, err := resolveProtectedDirectory(item.path)
		if err != nil {
			fail(w, 503, "Could not check protected directories. Check registered paths and their permissions.")
			return false
		}
		for _, path := range paths {
			if directoryContainsPath(path, resolved) || item.tree && directoryContainsPath(resolved, path) {
				fail(w, 409, "This folder is protected: it contains the engine user's home, engine data or a registered project.")
				return false
			}
		}
	}
	return r.Context().Err() == nil
}

func directoryMutationError(w http.ResponseWriter, err error, deleting bool) {
	switch {
	case errors.Is(err, os.ErrExist), errors.Is(err, syscall.ENOTEMPTY):
		if deleting {
			fail(w, 409, "This folder is not empty. Move or remove its contents before deleting it.")
		} else {
			fail(w, 409, "A file or folder with this name already exists. Choose another name.")
		}
	case errors.Is(err, os.ErrPermission):
		fail(w, 403, "The engine user cannot change this folder. Check its permissions.")
	case errors.Is(err, os.ErrNotExist):
		fail(w, 404, "The folder or its parent no longer exists. Refresh this directory.")
	case errors.Is(err, syscall.ENOTDIR):
		fail(w, 400, "The path is no longer a directory. Refresh this directory.")
	case errors.Is(err, errors.ErrUnsupported):
		fail(w, 501, "Safe folder renaming is not supported by this engine's filesystem.")
	default:
		fail(w, 500, "The folder change could not be confirmed. Refresh this directory before trying again.")
	}
}

func (a *app) renameFilesystemDirectory(w http.ResponseWriter, r *http.Request) {
	body, source, valid := readDirectoryMutation(w, r)
	if !valid {
		return
	}
	if !validNewDirectoryName(body.NewName) {
		fail(w, 400, "Choose a valid folder name without path separators or control characters (at most 255 bytes).")
		return
	}
	destination := filepath.Join(body.Parent, body.NewName)
	if len(destination) > directoryPathBytes || filepath.Dir(destination) != body.Parent {
		fail(w, 400, "The new folder path is invalid or too long.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowDirectoryMutationLocked(w, r, source, destination) {
		return
	}
	if source != destination {
		// Atomic no-replace: an existence check followed by os.Rename would
		// overwrite another empty folder on Unix, including under a race.
		if err := renameDirectoryNoReplace(source, destination); err != nil {
			directoryMutationError(w, err, false)
			return
		}
	}
	respond(w, 200, filesystemDirectory{Name: body.NewName, Path: destination})
}

func (a *app) deleteFilesystemDirectory(w http.ResponseWriter, r *http.Request) {
	body, source, valid := readDirectoryMutation(w, r)
	if !valid {
		return
	}
	if body.Confirmation != body.Name {
		fail(w, 400, "Confirm the exact folder before deleting it.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowDirectoryMutationLocked(w, r, source, "") {
		return
	}
	// Directory-only, nonrecursive removal: never delete files, traverse links
	// or remove contents, even when an external writer changes this entry.
	if err := removeEmptyDirectory(source); err != nil {
		directoryMutationError(w, err, true)
		return
	}
	respond(w, 200, struct {
		Path string `json:"path"`
	}{source})
}
