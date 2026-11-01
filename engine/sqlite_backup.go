package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var sqliteLegacyFiles = [...]string{"state.json", "stream.sealed", "stream.journal", "client-auth.json", "engine-access.json"}

type legacyFileFingerprint struct {
	Present bool   `json:"present"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type legacyBackupManifest struct {
	Version int                              `json:"version"`
	ID      string                           `json:"id"`
	Files   map[string]legacyFileFingerprint `json:"files"`
}

// Hooks are explicit per-operation dependencies for crash-boundary tests, never
// global environment flags or an HTTP migration-control surface.
type sqliteMigrationHook func(boundary string) error

func (h sqliteMigrationHook) reach(boundary string) error {
	if h != nil {
		return h(boundary)
	}
	return nil
}

func fingerprintLegacyFile(path string) (legacyFileFingerprint, error) {
	var result legacyFileFingerprint
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("legacy source is not a regular file")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err != nil {
		return result, err
	}
	if n != info.Size() {
		return result, errors.New("legacy source changed during hashing")
	}
	return legacyFileFingerprint{Present: true, Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func verifyLegacyFiles(dir string, manifest legacyBackupManifest) error {
	if manifest.Version != 1 || !validMigrationID(manifest.ID) || len(manifest.Files) != len(sqliteLegacyFiles) {
		return errors.New("invalid legacy backup manifest")
	}
	for _, name := range sqliteLegacyFiles {
		expected, ok := manifest.Files[name]
		if !ok {
			return fmt.Errorf("legacy backup manifest missing %s", name)
		}
		actual, err := fingerprintLegacyFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("legacy source fingerprint mismatch: %s", name)
		}
	}
	return nil
}

func validMigrationID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == id
}

// Caller holds the engine data-directory lock. Capture precedes all legacy
// normalization, replay, auth writes and outbox recovery. Never delete even a
// failed/incomplete capture: it is evidence, not a reusable committed manifest.
func captureLegacyBackup(dir string, hook sqliteMigrationHook) (legacyBackupManifest, error) {
	manifest := legacyBackupManifest{Version: 1, ID: newID(), Files: map[string]legacyFileFingerprint{}}
	if !filepath.IsAbs(dir) {
		return manifest, errors.New("backup requires absolute data directory")
	}
	root := filepath.Join(dir, "legacy-backups")
	if err := os.MkdirAll(root, 0700); err != nil {
		return manifest, err
	}
	backup := filepath.Join(root, manifest.ID)
	if err := os.Mkdir(backup, 0700); err != nil {
		return manifest, err
	}
	for _, name := range sqliteLegacyFiles {
		source := filepath.Join(dir, name)
		expected, err := fingerprintLegacyFile(source)
		if err != nil {
			return manifest, err
		}
		manifest.Files[name] = expected
		if !expected.Present {
			continue
		}
		if err := copyLegacyFile(source, filepath.Join(backup, name)); err != nil {
			return manifest, err
		}
		if err := hook.reach("backup-file:" + name); err != nil {
			return manifest, err
		}
	}
	if err := verifyLegacyFiles(backup, manifest); err != nil {
		return manifest, err
	}
	if err := verifyLegacyFiles(dir, manifest); err != nil {
		return manifest, err
	}
	if err := writeSQLiteMarker(filepath.Join(backup, "manifest.json"), manifest); err != nil {
		return manifest, err
	}
	return manifest, hook.reach("backup-manifest")
}

func copyLegacyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dest, source)
	return errors.Join(copyErr, dest.Sync(), dest.Close())
}

// Small, versioned metadata only. No state/domain/history passes this helper.
func writeSQLiteMarker(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sqlite-marker-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(encoded)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}
