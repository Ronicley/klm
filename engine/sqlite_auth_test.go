package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSQLiteMigrationAuthPreservesCredentialsAndSavedEmptyPolicy(t *testing.T) {
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "")
	t.Setenv("KLM_ALLOWED_ORIGINS", "https://bootstrap.example")
	t.Setenv("KLM_TRUSTED_TLS_PROXIES", "10.0.0.1/32")
	ctx := context.Background()
	dir := t.TempDir()
	secret, err := authSecret()
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Add(-time.Hour)
	legacy := clientAuthDisk{Clients: []pairedClient{{ID: "preserved-client", Name: "Existing desktop", Hash: authHash(secret), CreatedAt: stamp.Add(-24 * time.Hour), LastUsedAt: stamp}}}
	body, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "client-auth.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "engine-access.json"), []byte(" {\r\n\"allowedOrigins\":[],\"trustedTlsProxies\":[] }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := captureLegacyBackup(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "legacy-backups", manifest.ID)
	store, path := sqliteTestStore(t)
	if err := importSQLiteAuth(ctx, store, backup); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.loadPairedClients(ctx)
	if err != nil || !reflect.DeepEqual(loaded, legacy) {
		t.Fatalf("credential identity/time lost: %+v %v", loaded, err)
	}
	auth, err := newClientAuthStorage(dir, store)
	if err != nil {
		t.Fatal(err)
	}
	if auth.devOpenAccess || len(auth.savedAccess.AllowedOrigins) != 0 || len(auth.savedAccess.TrustedTLSProxies) != 0 || auth.origins["https://bootstrap.example"] {
		t.Fatal("saved empty policy fell back to environment")
	}
	request := httptest.NewRequest("GET", "http://localhost/api/state", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	_, done, ok := auth.admit(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("imported credential requires re-pairing")
	}
	done()
	updated := auth.disk.Clients[0].LastUsedAt
	if !updated.After(stamp) {
		t.Fatal("authenticated use not updated")
	}
	if err := verifyLegacyFiles(dir, manifest); err != nil {
		t.Fatal("SQL auth wrote root legacy files:", err)
	}
	if err := verifyLegacyFiles(backup, manifest); err != nil {
		t.Fatal(err)
	}
	// A failed persistence mutation must not revoke an in-memory credential.
	if _, err := store.writer.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if err := auth.revoke("preserved-client"); err == nil || len(auth.disk.Clients) != 1 {
		t.Fatal("failed SQL revocation published")
	}
	if _, err := store.writer.Exec(`PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	again, err := newClientAuthStorage(dir, reopened)
	if err != nil {
		t.Fatal(err)
	}
	if !again.disk.Clients[0].LastUsedAt.Equal(updated) {
		t.Fatal("last-use not durable")
	}
	_, done, ok = again.admit(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("credential failed after reopen")
	}
	done()
	if err := again.revoke("preserved-client"); err != nil {
		t.Fatal(err)
	}
	if clients, err := reopened.loadPairedClients(ctx); err != nil || len(clients.Clients) != 0 {
		t.Fatal("revocation not durable", err)
	}
	if err := verifyLegacyFiles(backup, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMigrationAuthBootstrapAndAtomicity(t *testing.T) {
	t.Setenv("KLM_ALLOWED_ORIGINS", "https://bootstrap.example")
	t.Setenv("KLM_TRUSTED_TLS_PROXIES", "")
	ctx := context.Background()
	store, _ := sqliteTestStore(t)
	if err := importSQLiteAuth(ctx, store, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	settings, err := store.loadEngineAccess(ctx)
	if err != nil || !reflect.DeepEqual(settings.AllowedOrigins, []string{"https://bootstrap.example"}) {
		t.Fatalf("missing config bootstrap: %+v %v", settings, err)
	}
	if err := store.saveEngineAccess(ctx, engineAccessSettings{AllowedOrigins: []string{}, TrustedTLSProxies: []string{}}); err != nil {
		t.Fatal(err)
	}
	settings, err = store.loadEngineAccess(ctx)
	if err != nil || len(settings.AllowedOrigins) != 0 {
		t.Fatal("explicit empty config not retained", err)
	}
	other, _ := sqliteTestStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client-auth.json"), []byte(`{"clients":[{"id":"same"},{"id":"same"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := importSQLiteAuth(ctx, other, dir); err == nil {
		t.Fatal("duplicate clients imported")
	}
	var count int
	if err := other.writer.QueryRow(`SELECT count(*) FROM paired_clients`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial auth import committed", err)
	}
}
