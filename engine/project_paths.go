package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitignore "github.com/denormal/go-gitignore"
)

type ProjectPath struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	score int
}

func (a *app) projectPaths(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.ReplaceAll(r.URL.Query().Get("q"), "\\", "/"))
	if len(query) > 1024 {
		fail(w, 400, "Path query is too long.")
		return
	}
	var root string
	if a.store != nil {
		p, err := a.store.readProjectMeta(r.Context(), r.PathValue("id"))
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
		if p.Removed {
			fail(w, 404, "Project not found.")
			return
		}
		root = p.Folder
	} else {
		a.mu.Lock()
		p := a.state.project(r.PathValue("id"))
		if p == nil || p.Removed {
			a.mu.Unlock()
			fail(w, 404, "Project not found.")
			return
		}
		root = p.Folder
		a.mu.Unlock()
	}
	fs, err := os.OpenRoot(root)
	if err != nil {
		fail(w, 400, "Project directory cannot be opened.")
		return
	}
	defer fs.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
	defer cancel()
	results := []ProjectPath{}
	partial, visited := false, 0
	type directory struct {
		path    string
		ignores []gitignore.GitIgnore
		depth   int
	}
	queue := []directory{{path: "."}}
	for len(queue) > 0 {
		if ctx.Err() != nil || visited >= 20000 {
			partial = true
			break
		}
		dir := queue[0]
		queue = queue[1:]
		ignores := dir.ignores
		ignorePath := filepath.Join(dir.path, ".gitignore")
		if info, err := fs.Stat(ignorePath); err == nil {
			if !info.Mode().IsRegular() || info.Size() > 128<<10 {
				partial = true
				continue // do not discover through unknown ignore rules
			}
			f, err := fs.Open(ignorePath)
			if err != nil {
				partial = true
				continue
			}
			ignore := gitignore.New(io.LimitReader(f, 128<<10), dir.path, func(gitignore.Error) bool { partial = true; return true })
			f.Close()
			ignores = append(append([]gitignore.GitIgnore(nil), ignores...), ignore)
		} else if !os.IsNotExist(err) {
			partial = true
			continue
		}
		f, err := fs.Open(dir.path)
		if err != nil {
			partial = true
			continue
		}
		for {
			if ctx.Err() != nil || visited >= 20000 {
				partial = true
				break
			}
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				visited++
				if ctx.Err() != nil || visited > 20000 {
					partial = true
					break
				}
				if strings.EqualFold(entry.Name(), ".git") {
					continue
				}
				path := filepath.Join(dir.path, entry.Name())
				if !validRelativePath(filepath.ToSlash(path)) {
					continue
				}
				isDir, link := entry.IsDir(), entry.Type()&os.ModeSymlink != 0
				if link {
					info, err := fs.Stat(path)
					if err != nil {
						continue
					}
					isDir = info.IsDir()
					if !isDir && !info.Mode().IsRegular() {
						continue
					}
				} else if !isDir && !entry.Type().IsRegular() {
					continue
				}
				ignored := false
				for _, ignore := range ignores {
					rel, _ := filepath.Rel(ignore.Base(), path)
					if match := ignore.Relative(rel, isDir); match != nil {
						ignored = match.Ignore()
					}
				}
				if ignored {
					continue
				}
				path = filepath.ToSlash(path)
				score := pathScore(path, query)
				if query == "" && dir.path != "." {
					score = -1
				}
				if score >= 0 {
					kind := "file"
					if isDir {
						kind = "directory"
					}
					results = append(results, ProjectPath{Path: path, Kind: kind, score: score})
				}
				if query != "" && isDir && !link {
					if dir.depth >= 64 {
						partial = true
					} else {
						queue = append(queue, directory{path: path, ignores: ignores, depth: dir.depth + 1})
					}
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					partial = true
				}
				break
			}
		}
		f.Close()
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score < results[j].score
		}
		if results[i].Kind != results[j].Kind {
			return results[i].Kind == "directory"
		}
		return results[i].Path < results[j].Path
	})
	if len(results) > 40 {
		results, partial = results[:40], true
	}
	respond(w, 200, map[string]any{"paths": results, "partial": partial})
}

func pathScore(path, query string) int {
	path = strings.ToLower(path)
	name := filepath.Base(path)
	switch {
	case query == "", name == query:
		return 0
	case strings.HasPrefix(name, query):
		return 1
	case strings.HasPrefix(path, query):
		return 2
	case strings.Contains(name, query):
		return 3
	case strings.Contains(path, query):
		return 4
	}
	// Simple subsequence match supports short queries without an indexer.
	remaining := []rune(query)
	for _, char := range path {
		if len(remaining) > 0 && char == remaining[0] {
			remaining = remaining[1:]
		}
	}
	if len(remaining) == 0 {
		return 5
	}
	return -1
}
