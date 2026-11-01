package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Normal CLI startup has one authority. The legacy reader is reachable only from
// the unpublished importer; it never runs cleanup or rewrites originals first.
func serveSQLiteEngine(dir string, listener net.Listener) (result error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a := &app{dir: dir, ctx: ctx, sqlChanges: &sqliteChangeBus{}, runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}, graphRuns: map[string]*graphExecution{}, picker: make(chan struct{}, 1), webhookWake: make(chan struct{}, 1), webhookIngress: make(chan struct{}, 2)}
	shutdown := make(chan struct{})
	control, err := serveEngineControl(dir, a, shutdown)
	if err != nil {
		return err
	}
	defer control.Close()
	go func() {
		select {
		case <-shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	log.Print("Preparing SQLite storage; original upgrade sources remain in immutable legacy-backups.")
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, func(stage string) error { log.Printf("Storage initialization: %s", stage); return ctx.Err() })
	if err != nil {
		return err
	}
	a.store = store
	defer func() {
		a.mu.Lock()
		a.closing = true
		a.controlReady = false
		cancel()
		a.mu.Unlock()
		a.wg.Wait()
		a.shutdownSessionRuntimes()
		checkpointCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		result = errors.Join(result, a.storageErr, store.checkpoint(checkpointCtx), store.close())
	}()
	if err := store.recoverSQLGraphCatalog(ctx); err != nil {
		return err
	}
	if err := store.recoverSQLLibrary(ctx); err != nil {
		return err
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.initializeAnalytics() }); err != nil {
		return err
	}
	if err := store.runDelegationRecoveryBarrier(ctx, recoverDelegationRuntime); err != nil {
		return err
	}
	if err := recoverSQLiteRuntime(ctx, store, time.Now().UTC(), nil); err != nil {
		return err
	}
	for {
		n, _, err := store.cleanWebhookBatch(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	a.harnesses, a.binaries = discoverHarnesses()
	if err := a.startHarnessInstaller(); err != nil {
		return err
	}
	auth, err := newClientAuthStorage(dir, store)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.clientAuth = auth
	a.mu.Unlock()
	if auth.devOpenAccess {
		log.Print("Development mode: public client security disabled by KLM_DEV_DISABLE_CLIENT_AUTH.")
	}
	if _, err := a.ensureGeneralSession(); err != nil {
		return err
	}
	a.restartEngine = func() error { return spawnEngineRestart(dir) }
	if err := a.startActivityBoard(); err != nil {
		return err
	}
	for _, worker := range []func(){a.expireConsultations, a.webhookWorker, a.automationWorker, a.checkpointAnalytics, a.checkpointSQLRuntime} {
		a.wg.Add(1)
		go func(run func()) { defer a.wg.Done(); run() }(worker)
	}
	a.mu.Lock()
	a.scheduleLinkedLocked()
	a.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	server := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	a.mu.Lock()
	if ctx.Err() == nil {
		a.controlReady = true
	}
	a.mu.Unlock()
	log.Printf("KLM engine listening on http://%s (SQLite contract 3)", listener.Addr())
	select {
	case <-ctx.Done():
	case err = <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	a.mu.Lock()
	a.closing = true
	a.controlReady = false
	cancel()
	a.mu.Unlock()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	_ = server.Shutdown(shutdownCtx)
	_ = server.Close()
	return result
}
func (a *app) checkpointSQLRuntime() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.store.collectEventGenerations(a.ctx); err != nil {
				if a.ctx.Err() != nil {
					return
				}
				a.mu.Lock()
				a.failStorageLocked(err)
				a.mu.Unlock()
				return
			}
			var busy, frames, done int
			err := a.store.writer.QueryRowContext(a.ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &done)
			if err != nil {
				if a.ctx.Err() != nil {
					return
				}
				a.mu.Lock()
				a.failStorageLocked(err)
				a.mu.Unlock()
				return
			}
			if frames-done > 10000 {
				log.Printf("SQLite checkpoint waiting for readers (%d/%d frames)", done, frames)
			}
		}
	}
}
