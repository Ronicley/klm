package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	directoryPathBytes = 16 << 10
	directoryScanLimit = 10000
	directoryItemLimit = 1000
	directoryJSONLimit = 256 << 10
)

type filesystemDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type filesystemListing struct {
	Path        string                `json:"path"`
	Parent      *string               `json:"parent"`
	Directories []filesystemDirectory `json:"directories"`
	Incomplete  bool                  `json:"incomplete"`
	Skipped     int                   `json:"skipped"`
}

func browserDirectoryPath(path string) (string, bool) {
	if path == "" || len(path) > directoryPathBytes || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", false
	}
	return filepath.Clean(path), true
}

func (a *app) filesystemRoots(w http.ResponseWriter, r *http.Request) {
	roots, err := platformDirectoryRoots()
	if err != nil {
		fail(w, 500, "Could not load filesystem roots. Retry the request.")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	var home *string
	if candidate, err := os.UserHomeDir(); err == nil {
		if path, valid := browserDirectoryPath(candidate); valid {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				home = &path
				roots = append(roots, filesystemDirectory{Name: "Home", Path: path})
			}
		}
	}
	if r.Context().Err() != nil {
		return
	}
	respond(w, 200, struct {
		Roots        []filesystemDirectory `json:"roots"`
		Home         *string               `json:"home"`
		NativePicker bool                  `json:"nativePicker"`
	}{roots, home, platformNativeDirectoryPicker})
}

func directoryBrowserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, syscall.ENOTDIR):
		fail(w, 400, "The path is not a directory.")
	case errors.Is(err, os.ErrNotExist):
		fail(w, 404, "Directory no longer exists. Enter another path.")
	case errors.Is(err, os.ErrPermission):
		fail(w, 403, "The engine user cannot read this directory. Choose another directory.")
	default:
		fail(w, 500, "Could not read this directory. Retry or enter another path.")
	}
}

func (a *app) filesystemDirectories(w http.ResponseWriter, r *http.Request) {
	path, valid := browserDirectoryPath(r.URL.Query().Get("path"))
	if !valid {
		fail(w, 400, "Enter an absolute directory path (at most 16 KiB).")
		return
	}
	ctx := r.Context()
	if ctx.Err() != nil {
		return
	}
	info, err := os.Stat(path) // Follow aliases, but retain the lexical path for registration.
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		directoryBrowserError(w, err)
		return
	}
	if !info.IsDir() {
		fail(w, 400, "The path is not a directory.")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		directoryBrowserError(w, err)
		return
	}
	defer file.Close()
	result := filesystemListing{Path: path, Directories: []filesystemDirectory{}}
	parent := filepath.Dir(path)
	// Volume/share roots are terminal; never navigate above a UNC share.
	volumeRoot := filepath.Clean(filepath.VolumeName(path) + string(filepath.Separator))
	if path != volumeRoot && parent != path && filepath.IsAbs(parent) {
		result.Parent = &parent
	}
	// Budget the actual JSON encoding (including escaping), with worst-case metadata.
	budget := result
	budget.Skipped = directoryScanLimit
	encoded, _ := json.Marshal(budget)
	bytesUsed := len(encoded) + 1 // respond's trailing newline
	inspected := 0
scan:
	for inspected < directoryScanLimit {
		if ctx.Err() != nil {
			return
		}
		entries, readErr := file.ReadDir(min(128, directoryScanLimit-inspected))
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			// A partial I/O failure is an error, not a successful empty/partial scan.
			directoryBrowserError(w, readErr)
			return
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			inspected++
			name := entry.Name()
			child := filepath.Join(path, name)
			if !utf8.ValidString(name) || len(child) > directoryPathBytes {
				result.Skipped++
				result.Incomplete = true
				continue
			}
			isDirectory := entry.IsDir()
			if !isDirectory {
				// Type zero can mean an ordinary file OR unknown filesystem metadata.
				// Stat also follows directory symlinks and Windows junctions, without recursion.
				if entry.Type() == 0 || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					info, statErr := os.Stat(child)
					if ctx.Err() != nil {
						return
					}
					if statErr != nil {
						result.Skipped++
						result.Incomplete = true
						continue
					}
					isDirectory = info.IsDir()
				}
			}
			if !isDirectory {
				continue
			}
			item := filesystemDirectory{Name: name, Path: child}
			encoded, _ := json.Marshal(item)
			if bytesUsed+len(encoded)+1 > directoryJSONLimit {
				result.Incomplete = true
				break scan
			}
			bytesUsed += len(encoded) + 1
			result.Directories = append(result.Directories, item)
			if len(result.Directories) == directoryItemLimit {
				result.Incomplete = true
				break scan
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if inspected == directoryScanLimit {
			result.Incomplete = true
		}
	}
	sort.Slice(result.Directories, func(i, j int) bool { return result.Directories[i].Name < result.Directories[j].Name })
	if ctx.Err() != nil {
		return
	}
	respond(w, 200, result)
}

func validNewDirectoryName(name string) bool {
	if name == "" || strings.TrimSpace(name) == "" || len(name) > 255 || !utf8.ValidString(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	if strings.ContainsAny(name, `<>:"|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	base := strings.ToUpper(strings.TrimSpace(strings.SplitN(name, ".", 2)[0]))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	return !(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9')
}

func (a *app) createFilesystemDirectory(w http.ResponseWriter, r *http.Request) {
	var body struct{ Parent, Name string }
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if !decode(w, r, &body) {
		return
	}
	parent, valid := browserDirectoryPath(body.Parent)
	if !valid {
		fail(w, 400, "Enter an absolute parent directory path (at most 16 KiB).")
		return
	}
	if !validNewDirectoryName(body.Name) {
		fail(w, 400, "Choose a valid folder name without path separators or control characters (at most 255 bytes).")
		return
	}
	child := filepath.Join(parent, body.Name)
	if len(child) > directoryPathBytes || filepath.Dir(child) != parent {
		fail(w, 400, "The new folder path is invalid or too long.")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	info, err := os.Stat(parent)
	if err != nil {
		directoryBrowserError(w, err)
		return
	}
	if !info.IsDir() {
		fail(w, 400, "The parent path is not a directory.")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	// One explicit leaf only: no recursive parents, overwrite or privilege change.
	// Filesystem ownership, permissions and symlink resolution stay with the engine user.
	if err := os.Mkdir(child, 0755); err != nil {
		switch {
		case errors.Is(err, os.ErrExist):
			fail(w, 409, "A file or folder with this name already exists.")
		case errors.Is(err, os.ErrPermission):
			fail(w, 403, "The engine user cannot create a folder in this directory.")
		case errors.Is(err, os.ErrNotExist):
			fail(w, 404, "The parent directory no longer exists. Refresh and choose another directory.")
		case errors.Is(err, syscall.ENOTDIR):
			fail(w, 400, "The parent path is not a directory.")
		default:
			fail(w, 500, "Could not create the folder. Check its name and the engine user's permissions.")
		}
		return
	}
	respond(w, 201, filesystemDirectory{Name: body.Name, Path: child})
}
