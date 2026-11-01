package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const bootstrapNodeVersion = "22.22.0"

// Read from https://nodejs.org/dist/v22.22.0/SHASUMS256.txt during implementation.
// Runtime does not trust a freshly downloaded checksum or resolve latest.
func nodeArtifact() (string, string) {
	if runtime.GOOS == "windows" {
		return "node-v22.22.0-win-x64.zip", "c97fa376d2becdc8863fcd3ca2dd9a83a9f3468ee7ccf7a6d076ec66a645c77a"
	}
	return "node-v22.22.0-linux-x64.tar.gz", "c33c39ed9c80deddde77c960d00119918b9e352426fd604ba41638d6526a4744"
}

func downloadNode(ctx context.Context, dir string) (string, error) {
	name, digest := nodeArtifact()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://nodejs.org/dist/v"+bootstrapNodeVersion+"/"+name, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Node artifact redirect refused.") }}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("Could not download the official Node.js artifact. Check engine network/proxy access and retry.")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("Official Node.js download returned an unsuccessful response.")
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(response.Body, (100<<20)+1))
	err = errors.Join(copyErr, f.Close())
	if err != nil {
		return "", err
	}
	if n > 100<<20 || hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New("Node.js artifact integrity/size verification failed. Nothing was installed.")
	}
	return path, nil
}

// The destination is a newly created private staging directory, never an
// existing installation. Links/devices are not extracted; npm/npx links are
// published separately using fixed targets after verification.
func extractNode(ctx context.Context, archive, destination string) error {
	var total int64
	count := 0
	write := func(name string, mode os.FileMode, size int64, source io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		total += size
		if count > 20000 || size < 0 || total > 400<<20 {
			return errors.New("Node archive exceeds extraction limits.")
		}
		_, relative, ok := strings.Cut(strings.ReplaceAll(name, "\\", "/"), "/")
		if !ok || relative == "" {
			return nil
		}
		if strings.Contains(relative, ":") || !filepath.IsLocal(filepath.FromSlash(relative)) {
			return errors.New("Unsafe Node archive path.")
		}
		path := filepath.Join(destination, filepath.FromSlash(relative))
		if mode.IsDir() {
			return os.MkdirAll(path, 0755)
		}
		if !mode.IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0755)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(f, io.LimitReader(source, size+1))
		err = errors.Join(copyErr, f.Close())
		if n != size {
			return errors.New("Truncated Node archive member.")
		}
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		r, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer r.Close()
		for _, member := range r.File {
			if member.UncompressedSize64 > 400<<20 {
				return errors.New("Node archive member is too large.")
			}
			f, err := member.Open()
			if err != nil {
				return err
			}
			err = write(member.Name, member.Mode(), int64(member.UncompressedSize64), f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	r := tar.NewReader(gz)
	for {
		header, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := write(header.Name, header.FileInfo().Mode(), header.Size, r); err != nil {
			return err
		}
	}
}

func bootstrapNode(ctx context.Context, n nodeInstallation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prerequisiteBootstrapPermission(n); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "klm-node-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	archive, err := downloadNode(ctx, temp)
	if err != nil {
		return err
	}
	stage := filepath.Join(temp, "distribution")
	if err := os.Mkdir(stage, 0700); err != nil {
		return err
	}
	if err := extractNode(ctx, archive, stage); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.Version != "" {
		return publishMissingNPM(ctx, stage, n)
	}
	return publishNode(ctx, stage)
}

func prerequisiteBootstrapPermission(n nodeInstallation) error {
	if n.Version == "" {
		return bootstrapPermission()
	}
	// A broken npm tree/shim is an existing installation, never a missing one.
	if err := ensureAbsent(filepath.Dir(filepath.Dir(n.NPM))); err != nil {
		return errors.New("An existing npm directory needs repair with its original installer. It will not be overwritten implicitly.")
	}
	for _, name := range npmShimNames() {
		if err := ensureAbsent(filepath.Join(filepath.Dir(n.Node), name)); err != nil {
			return errors.New("An existing npm/npx entry conflicts with npm bootstrap. Repair it with its original installer, then refresh.")
		}
	}
	return nil
}
func npmShimNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"npm", "npm.cmd", "npm.ps1", "npx", "npx.cmd", "npx.ps1"}
	}
	return []string{"npm", "npx"}
}
func publishMissingNPM(ctx context.Context, stage string, n nodeInstallation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prerequisiteBootstrapPermission(n); err != nil {
		return err
	}
	destination := filepath.Dir(filepath.Dir(n.NPM))
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	source := filepath.Join(stage, "node_modules", "npm")
	if runtime.GOOS != "windows" {
		source = filepath.Join(stage, "lib", "node_modules", "npm")
	}
	if err := copyNodeTree(ctx, source, destination); err != nil {
		return err
	}
	for _, name := range npmShimNames() {
		target := filepath.Join(filepath.Dir(n.Node), name)
		if err := ctx.Err(); err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			if err := os.Symlink(filepath.Join(destination, "bin", name+"-cli.js"), target); err != nil {
				return err
			}
		} else {
			// Publish official shims for ordinary terminal use, never evaluate them.
			from := filepath.Join(stage, name)
			if !regularFile(from) {
				continue
			}
			in, err := os.Open(from)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
			if err != nil {
				in.Close()
				return err
			}
			_, err = io.Copy(out, in)
			err = errors.Join(err, in.Close(), out.Close())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type installPermissionError struct{ message string }

func (e *installPermissionError) Error() string { return e.message }
func needsInstallPermission(text string) error  { return &installPermissionError{text} }

// Metadata preflight is read-only. Actual creates still rely on OS permission
// checks and are classified as Needs permission when denied.
func ensureAbsent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("Node.js bootstrap destination already exists. Repair it using its original installer, then refresh: " + path)
	}
	return nil
}

// Copy into a new destination with O_EXCL; never overlay an existing tree or
// follow archive-provided symlinks into the global installation.
func copyNodeTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			return os.Mkdir(target, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("Unexpected link in staged Node distribution.")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		return errors.Join(err, out.Close())
	})
}
