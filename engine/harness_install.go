package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type harnessInstallPlan struct {
	Token          string           `json:"token"`
	HarnessID      string           `json:"harnessId"`
	Package        string           `json:"package"`
	Version        string           `json:"version"`
	CurrentVersion string           `json:"currentVersion"`
	Destination    string           `json:"destination"`
	Prerequisites  nodeInstallation `json:"prerequisites"`
	Replace        bool             `json:"replace"`
	Blocked        string           `json:"blocked,omitempty"`
	Noop           bool             `json:"noop"`
	Fingerprint    string           `json:"fingerprint"`
}

type harnessInstallJob struct {
	ID          string   `json:"id"`
	OperationID string   `json:"operationId"`
	HarnessID   string   `json:"harnessId"`
	PlanToken   string   `json:"planToken"`
	State       string   `json:"state"`
	Step        string   `json:"step"`
	Log         []string `json:"log"`
	Error       string   `json:"error,omitempty"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

func installJobActive(state string) bool {
	return state == "checking" || state == "installing_prerequisites" || state == "installing_harness" || state == "verifying"
}

type harnessInstallService struct {
	app            *app
	probeMu        sync.Mutex // Serializes inventory refresh and accepted plan revalidation.
	mu             sync.Mutex // Only immutable job/plan snapshots leave this lock.
	plans          []harnessInstallPlan
	jobs           []harnessInstallJob
	active         string
	inventoryReady bool
}

func (a *app) startHarnessInstaller() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.store.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS harness_installations (id TEXT PRIMARY KEY, operation_id TEXT NOT NULL UNIQUE, payload TEXT NOT NULL CHECK(length(payload)<=32768))`)
		return err
	}); err != nil {
		return err
	}
	s := &harnessInstallService{app: a, jobs: []harnessInstallJob{}, plans: []harnessInstallPlan{}}
	rows, err := a.store.readers.QueryContext(ctx, `SELECT payload FROM harness_installations ORDER BY rowid`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var job harnessInstallJob
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			rows.Close()
			return err
		}
		s.jobs = append(s.jobs, job)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for i := range s.jobs {
		job := s.jobs[i]
		if installJobActive(job.State) {
			job.State, job.Step, job.Error = "interrupted", "Interrupted", "The engine stopped before installation was confirmed. Refresh and inspect the global installation before an explicit retry."
			job.UpdatedAt = now()
			if err := s.persist(job); err != nil {
				return err
			}
			s.jobs[i] = job
		}
	}
	a.harnessInstaller = s
	a.mu.Lock()
	for id, b := range a.binaries {
		b.app = a
		a.binaries[id] = b
	}
	a.mu.Unlock()
	return nil
}

func (s *harnessInstallService) persist(job harnessInstallJob) error {
	changes, err := s.writeJob(job)
	if err == nil && s.app.sqlChanges != nil {
		s.app.sqlChanges.publish(changes)
	}
	return err
}

func (s *harnessInstallService) writeJob(job harnessInstallJob) (sqliteSessionChanges, error) {
	s.app.mu.Lock()
	defer s.app.mu.Unlock()
	return s.writeJobLocked(job, false)
}

// app.mu serializes the frozen-state decision with failStorageLocked. Owned
// finalizers may record interruption during shutdown, but never after storage
// has been latched read-only, even if the underlying SQLite writer still works.
func (s *harnessInstallService) writeJobLocked(job harnessInstallJob, finalizing bool) (sqliteSessionChanges, error) {
	if s.app.storageErr != nil {
		return sqliteSessionChanges{}, controlError(503, "Engine storage is read-only until restarted. Harness installation is unavailable.")
	}
	if !finalizing {
		if err := s.app.harnessInstallStateErrorLocked(nil); err != nil {
			return sqliteSessionChanges{}, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := json.Marshal(job)
	if err != nil {
		return sqliteSessionChanges{}, err
	}
	changes, err := s.app.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.tx.ExecContext(ctx, `INSERT INTO harness_installations(id,operation_id,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, job.ID, job.OperationID, string(data))
		if err != nil {
			return err
		}
		st.changes.Invalidated["harnesses"] = true
		return nil
	})
	if err != nil {
		return sqliteSessionChanges{}, s.app.failStorageLocked(err)
	}
	return changes, nil
}

func (a *app) harnessInstallStateErrorLocked(ctx context.Context) error {
	if a.storageErr != nil {
		return controlError(503, "Engine storage is read-only until restarted. Harness installation is unavailable.")
	}
	if a.closing || a.ctx != nil && a.ctx.Err() != nil || ctx != nil && ctx.Err() != nil {
		return controlError(503, "Harness installation is unavailable while the engine is stopping or its request is cancelled.")
	}
	return nil
}

func (a *app) checkHarnessInstallState(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.harnessInstallStateErrorLocked(ctx)
}

func (s *harnessInstallService) setJob(ctx context.Context, id, state, step, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, previous := range s.jobs {
		if previous.ID == id {
			job := previous
			job.Log = append([]string(nil), previous.Log...)
			job.State, job.Step, job.UpdatedAt = state, step, now()
			if message != "" {
				job.Log = append(job.Log, message)
				if len(job.Log) > 24 {
					job.Log = job.Log[len(job.Log)-24:]
				}
			}
			if state == "failed" || state == "needs_permission" || state == "interrupted" {
				job.Error = message
			}
			s.app.mu.Lock()
			err := s.app.harnessInstallStateErrorLocked(ctx)
			var changes sqliteSessionChanges
			if err == nil {
				changes, err = s.writeJobLocked(job, false)
			}
			s.app.mu.Unlock()
			if err != nil {
				return err
			}
			s.jobs[i] = job
			if s.app.sqlChanges != nil {
				s.app.sqlChanges.publish(changes)
			}
			return nil
		}
	}
	return errors.New("Installation job not found.")
}

func (s *harnessInstallService) refresh(ctx context.Context) ([]Harness, []harnessInstallPlan, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if err := s.app.checkHarnessInstallState(ctx); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	active := s.active != ""
	plans := append([]harnessInstallPlan{}, s.plans...)
	s.mu.Unlock()
	if active {
		return s.app.harnessSnapshot(), plans, nil
	}
	release, err := s.app.acquireHarnessConsumer()
	if err != nil {
		return nil, nil, err
	}
	defer release()
	n := resolveNode(ctx)
	hs, bs := discoverWithNode(ctx, n)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	plans = buildHarnessInstallPlans(n, hs, harnessInstallPlatform())
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.plans = plans
	s.inventoryReady = true
	s.mu.Unlock()
	if err := s.app.publishHarnessInventory(hs, bs); err != nil {
		return nil, nil, err
	}
	return hs, plans, nil
}

// Builds consent plans from an already probed snapshot. Finalization reuses its
// verified Node/inventory rather than spawning another successful discovery.
func buildHarnessInstallPlans(n nodeInstallation, hs []Harness, platformReason string) []harnessInstallPlan {
	plans := make([]harnessInstallPlan, 0, len(hs))
	for _, h := range hs {
		recipe, ok := harnessRecipeFor(h.ID)
		if !ok {
			continue
		}
		p := harnessInstallPlan{HarnessID: recipe.ID, Package: recipe.Package, Version: recipe.Version, Destination: n.Prefix, Prerequisites: n, Noop: h.Available && h.Compatible}
		packageDir := filepath.Join(n.Root, filepath.FromSlash(recipe.Package))
		p.CurrentVersion = packageVersion(packageDir, recipe.Package)
		_, statErr := os.Lstat(packageDir)
		p.Replace = statErr == nil && !p.Noop
		p.Fingerprint = fileFingerprint(n.Node) + "|" + fileFingerprint(n.NPM) + "|" + fileFingerprint(filepath.Join(packageDir, "package.json")) + "|" + fileFingerprint(h.Path) + "|" + h.InstalledVersion + "|" + h.Source
		p.Blocked = platformReason
		if p.Blocked == "" {
			p.Blocked = n.Error
		}
		if p.Blocked == "" && n.Bootstrap {
			if e := prerequisiteBootstrapPermission(n); e != nil {
				p.Blocked = e.Error()
			}
		}
		if override := os.Getenv("KLM_" + strings.ToUpper(recipe.ID) + "_BIN"); override != "" && !p.Noop {
			p.Blocked = "Effective override KLM_" + strings.ToUpper(recipe.ID) + "_BIN blocks global discovery. Remove or repair that engine override, then refresh."
		}
		if h.Path != "" && !p.Noop {
			// Do not silently install alongside an unrelated PATH/standalone copy.
			if b, ok := packageBinary(recipe, n.Root, n); !ok || binaryDisplayPath(b) != h.Path {
				p.Blocked = "The effective executable is outside the target npm global package. Resolve that PATH/installation conflict before installing: " + h.Path
			}
		}
		encoded, _ := json.Marshal(p)
		sum := sha256.Sum256(encoded)
		p.Token = hex.EncodeToString(sum[:])
		plans = append(plans, p)
	}
	return plans
}

func binaryDisplayPath(b binary) string {
	if len(b.args) > 0 {
		return b.args[0]
	}
	return b.path
}
func (a *app) harnessSnapshot() []Harness {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Harness{}, a.harnesses...)
}
func (a *app) binarySnapshot() map[string]binary {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make(map[string]binary, len(a.binaries))
	for id, b := range a.binaries {
		result[id] = b
	}
	return result
}

func (a *app) publishHarnessInventory(hs []Harness, bs map[string]binary) error {
	a.mu.Lock()
	if err := a.harnessInstallStateErrorLocked(nil); err != nil {
		a.mu.Unlock()
		return err
	}
	changed := false
	before, _ := json.Marshal(a.harnesses)
	after, _ := json.Marshal(hs)
	changed = string(before) != string(after)
	if !changed {
		for id, b := range bs {
			old := a.binaries[id]
			if old.path != b.path || old.node != b.node || strings.Join(old.args, "\x00") != strings.Join(b.args, "\x00") {
				changed = true
				break
			}
		}
	}
	a.replaceHarnessInventoryLocked(hs, bs, changed)
	a.mu.Unlock()
	if !changed {
		return nil
	}
	a.invalidateHarnessCaches()
	if a.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.mu.Lock()
		err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error { st.changes.Invalidated["harnesses"] = true; return nil })
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *app) replaceHarnessInventoryLocked(hs []Harness, bs map[string]binary, changed bool) {
	for id, b := range bs {
		b.app = a
		bs[id] = b
	}
	a.harnesses, a.binaries = hs, bs
	if changed {
		a.graphValidation = nil
		for listener := range a.updateListeners {
			listener.all = true
			select {
			case listener.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (a *app) invalidateHarnessCaches() {
	// Never take quotaMu under app.mu: quota RPC admission takes the reverse.
	a.catalogMu.Lock()
	a.catalogs = nil
	for key, flight := range a.catalogFlights {
		flight.cancel()
		delete(a.catalogFlights, key)
	}
	a.catalogMu.Unlock()
	a.quotaMu.Lock()
	a.quotas = nil
	a.quotaMu.Unlock()
	codexLibraryVersions.Clear()
}

// Every process-level consumer participates, even after copying an immutable
// binary out of app.mu. An accepted installer reserves the same gate before
// releasing app.mu; no new probe or harness can race the busy check.
func (a *app) acquireHarnessConsumer() (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.harnessInstalling {
		return nil, errors.New("A global harness installation is in progress. Retry after it finishes.")
	}
	if a.closing || a.storageErr != nil {
		return nil, errors.New("Harness admission is unavailable while the engine is stopping or storage has failed.")
	}
	if a.ctx != nil && a.ctx.Err() != nil {
		return nil, errors.New("Harness admission is unavailable while the engine is stopping.")
	}
	a.harnessConsumers.Add(1)
	var once sync.Once
	return func() { once.Do(func() { a.harnessConsumers.Add(-1) }) }, nil
}
func (b binary) use() (func(), error) {
	if b.app == nil {
		return func() {}, nil
	}
	return b.app.acquireHarnessConsumer()
}

func (a *app) harnessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/harnesses", a.getHarnessInstallInventory)
	mux.HandleFunc("POST /api/harnesses/{harnessId}/install", a.installHarnessHTTP)
	mux.HandleFunc("GET /api/harness-installations/{jobId}", a.getHarnessInstallation)
	mux.HandleFunc("GET /api/harness-installations", a.lookupHarnessInstallation)
}

func (a *app) getHarnessInstallInventory(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	storageErr := a.storageErr
	a.mu.Unlock()
	if storageErr != nil {
		fail(w, 503, storageErr.Error())
		return
	}
	if a.harnessInstaller == nil {
		fail(w, 503, "Harness installation service is unavailable.")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	s := a.harnessInstaller
	s.mu.Lock()
	cached := s.inventoryReady || len(s.plans) > 0 || s.active != ""
	s.mu.Unlock()
	if r.URL.Query().Get("cached") != "true" || !cached {
		if _, _, err := s.refresh(ctx); err != nil {
			sqlHTTPError(w, err)
			return
		}
	}
	// Read one authoritative snapshot, rather than combining a pre-finalization
	// refresh result with a post-finalization completed receipt.
	s.mu.Lock()
	a.mu.Lock()
	hs := append([]Harness{}, a.harnesses...)
	plans := append([]harnessInstallPlan{}, s.plans...)
	jobs := append([]harnessInstallJob{}, s.jobs...)
	active := s.active
	a.mu.Unlock()
	s.mu.Unlock()
	host, _ := os.Hostname()
	if hs == nil {
		hs = []Harness{}
	}
	if plans == nil {
		plans = []harnessInstallPlan{}
	}
	respond(w, 200, map[string]any{"harnesses": hs, "plans": plans, "jobs": jobs, "activeJobId": active, "host": host, "platform": runtime.GOOS + "/" + runtime.GOARCH})
}

func (a *app) getHarnessInstallation(w http.ResponseWriter, r *http.Request) {
	a.harnessInstallationResponse(w, r, r.PathValue("jobId"), "")
}
func (a *app) lookupHarnessInstallation(w http.ResponseWriter, r *http.Request) {
	operation := r.URL.Query().Get("operationId")
	if operation == "" {
		fail(w, 400, "operationId is required.")
		return
	}
	a.harnessInstallationResponse(w, r, "", operation)
}
func (a *app) harnessInstallationResponse(w http.ResponseWriter, r *http.Request, id, operation string) {
	if a.harnessInstaller == nil {
		fail(w, 503, "Harness installation service is unavailable.")
		return
	}
	s := a.harnessInstaller
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		if id != "" && job.ID == id || operation != "" && job.OperationID == operation {
			respond(w, 200, job)
			return
		}
	}
	fail(w, 404, "Installation receipt not found. No installation was retried.")
}

type harnessInstallRequest struct {
	OperationID          string `json:"operationId"`
	PlanToken            string `json:"planToken"`
	ConfirmGlobal        bool   `json:"confirmGlobal"`
	ConfirmReplacement   bool   `json:"confirmReplacement"`
	ConfirmPrerequisites bool   `json:"confirmPrerequisites"`
}

func (a *app) installHarnessHTTP(w http.ResponseWriter, r *http.Request) {
	var body harnessInstallRequest
	if !decode(w, r, &body) {
		return
	}
	recipe, ok := harnessRecipeFor(r.PathValue("harnessId"))
	if !ok {
		fail(w, 404, "Unknown harness.")
		return
	}
	if len(body.OperationID) < 16 || len(body.OperationID) > 128 || strings.IndexFunc(body.OperationID, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-')
	}) >= 0 {
		fail(w, 400, "A stable alphanumeric operationId (16–128 characters) is required.")
		return
	}
	s := a.harnessInstaller
	if s == nil {
		fail(w, 503, "Harness installation service is unavailable.")
		return
	}
	s.mu.Lock()
	for _, job := range s.jobs {
		if job.OperationID == body.OperationID {
			s.mu.Unlock()
			if job.HarnessID != recipe.ID || job.PlanToken != body.PlanToken {
				fail(w, 409, "Operation ID already belongs to a different installation request.")
				return
			}
			respond(w, 202, job)
			return
		}
	}
	busy := s.active != ""
	s.mu.Unlock()
	if err := a.checkHarnessInstallState(r.Context()); err != nil {
		sqlHTTPError(w, err)
		return
	}
	if busy {
		fail(w, 409, "Another global installation is in progress.")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(70 * time.Second))
	// Refresh before accepting consent. The deterministic token covers actual
	// paths, versions, file identities, destination and prerequisite changes.
	_, plans, err := s.refresh(r.Context())
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	var plan harnessInstallPlan
	for _, p := range plans {
		if p.HarnessID == recipe.ID {
			plan = p
		}
	}
	if plan.Token == "" || plan.Token != body.PlanToken {
		fail(w, 409, "Installation plan changed. Refresh and confirm the new plan.")
		return
	}
	if plan.Blocked != "" {
		fail(w, 409, plan.Blocked)
		return
	}
	if !body.ConfirmGlobal || plan.Replace && !body.ConfirmReplacement || plan.Prerequisites.Bootstrap && !body.ConfirmPrerequisites {
		fail(w, 400, "Explicit consent for global scope, prerequisites and any version replacement is required.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Recheck operation identity and busy state after bounded probes.
	for _, job := range s.jobs {
		if job.OperationID == body.OperationID {
			if job.HarnessID != recipe.ID || job.PlanToken != body.PlanToken {
				fail(w, 409, "Operation ID conflicts with an accepted request.")
				return
			}
			respond(w, 202, job)
			return
		}
	}
	if s.active != "" {
		fail(w, 409, "Another global installation is in progress.")
		return
	}
	if len(s.jobs) >= 128 {
		fail(w, 409, "Installation receipt capacity reached (128). No global changes were made.")
		return
	}
	a.mu.Lock()
	// Conservative across all harnesses because npm and Node are shared. Retained
	// runtimes count as in use, including workloads surviving a completed turn.
	// Recheck after probes, atomically with reservation: a stale plan never
	// reopens the latched storage/closing/context admission gate.
	if err := a.harnessInstallStateErrorLocked(r.Context()); err != nil {
		a.mu.Unlock()
		sqlHTTPError(w, err)
		return
	}
	if a.closing || a.harnessConsumers.Load() > 0 || len(a.runs) > 0 || len(a.runtimes) > 0 || len(a.graphRuns) > 0 || len(a.graphStarting) > 0 {
		a.mu.Unlock()
		fail(w, 409, "Harnesses are in use. Stop active and idle retained runtimes and let catalog probes finish, then retry.")
		return
	}
	a.harnessInstalling = true
	installCtx, installCancel := context.WithCancel(a.ctx)
	a.harnessInstallCancel = installCancel
	a.wg.Add(1) // Reserve before releasing mu, including the preacceptance work.
	savedHarnesses, savedBinaries := a.harnesses, a.binaries
	// The internal inventory still means installed, not execution-admitted. Keep
	// it intact so new user messages can queue without being called uninstalled.
	a.harnessAdmissionStarted = time.Now()
	reserved := append([]Harness(nil), a.harnesses...)
	for i := range reserved {
		reserved[i].Available = false
		reserved[i].Error = "A global harness installation is in progress."
	}
	a.harnesses = reserved
	a.mu.Unlock()
	started := false
	defer func() {
		if !started {
			a.wg.Done()
		}
	}()
	unlockAdmission := func() {
		a.mu.Lock()
		installCancel()
		a.harnessInstallCancel = nil
		if !started {
			a.harnesses, a.binaries = savedHarnesses, savedBinaries
		}
		if !a.closing && a.storageErr == nil && a.ctx.Err() == nil {
			if err := a.endHarnessAdmissionPauseLocked(time.Now()); err != nil {
				a.failStorageLocked(err)
			}
		}
		a.harnessInstalling = false
		a.mu.Unlock()
		a.resumeHarnessAdmissions()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		unlockAdmission()
		fail(w, 500, "Could not resolve the engine user home.")
		return
	}
	lockDir := filepath.Join(home, ".klm")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		unlockAdmission()
		fail(w, 403, "Cannot create the shared engine-user installation lock.")
		return
	}
	lockPath := filepath.Join(lockDir, "harness-install.lock")
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		unlockAdmission()
		fail(w, 409, "The shared installation lock must be a regular file, not a link or device.")
		return
	}
	lock, err := lockDataDir(lockPath)
	if err != nil {
		unlockAdmission()
		fail(w, 409, "Another engine installer holds the shared global installation lock, or its lock is inaccessible.")
		return
	}
	job := harnessInstallJob{ID: newID(), OperationID: body.OperationID, HarnessID: recipe.ID, PlanToken: plan.Token, State: "checking", Step: "Checking prerequisites", CreatedAt: now(), UpdatedAt: now(), Log: []string{"Accepted global installation on the engine computer."}}
	if err := s.persist(job); err != nil {
		lock.Close()
		unlockAdmission()
		sqlHTTPError(w, err)
		return
	}
	s.jobs = append(s.jobs, job)
	s.active = job.ID
	started = true
	releaseLock := sync.OnceFunc(func() { _ = lock.Close() })
	go func() {
		defer a.wg.Done()
		defer releaseLock()
		defer installCancel()
		s.run(installCtx, job, recipe, plan, releaseLock)
	}()
	respond(w, 202, job)
}

func (s *harnessInstallService) run(parent context.Context, job harnessInstallJob, recipe harnessRecipe, plan harnessInstallPlan, releaseLock func()) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	var finalHarnesses []Harness
	var finalBinaries map[string]binary
	var finalNode nodeInstallation
	mutationStarted := false
	message := "Verified " + recipe.Name + " " + recipe.Version + ". Harness selectors have been refreshed."
	err := func() error {
		if err := s.app.checkHarnessInstallState(ctx); err != nil {
			return err
		}
		n := plan.Prerequisites
		// The cross-process mutation lock is now held. Revalidate the consented
		// paths/versions once more before even downloading prerequisites.
		current := resolveNode(ctx)
		hs, bs := discoverWithNode(ctx, current)
		if err := ctx.Err(); err != nil {
			return err
		}
		finalHarnesses, finalBinaries = hs, bs
		finalNode = current
		for _, h := range hs {
			if h.ID == recipe.ID {
				packageDir := filepath.Join(current.Root, filepath.FromSlash(recipe.Package))
				fingerprint := fileFingerprint(current.Node) + "|" + fileFingerprint(current.NPM) + "|" + fileFingerprint(filepath.Join(packageDir, "package.json")) + "|" + fileFingerprint(h.Path) + "|" + h.InstalledVersion + "|" + h.Source
				if current != n || fingerprint != plan.Fingerprint {
					return errors.New("Installation plan changed after acceptance. Refresh and confirm the actual paths, versions and prerequisites; no mutation was started.")
				}
			}
		}
		if plan.Noop {
			message = "The required version is already effective; no global mutation was needed."
			return nil
		}
		if err := s.app.checkHarnessInstallState(ctx); err != nil {
			return err
		}
		if err := checkInstallWriteAccess(ctx, n, recipe); err != nil {
			return err
		}
		if n.Bootstrap {
			step := "Installing Node.js"
			if n.Version != "" {
				step = "Installing npm"
			}
			if err := s.setJob(ctx, job.ID, "installing_prerequisites", step, "Downloading and verifying official Node.js "+bootstrapNodeVersion+" distribution (npm 10.9.4). Existing compatible Node.js is preserved."); err != nil {
				return err
			}
			if err := s.app.checkHarnessInstallState(ctx); err != nil {
				return err
			}
			mutationStarted = true
			if err := bootstrapNode(ctx, n); err != nil {
				return err
			}
			n = resolveNode(ctx)
			if n.Bootstrap || n.Error != "" {
				return errors.New("Node.js bootstrap could not be verified. Inspect the conventional installation and refresh before retrying.")
			}
			if n.Prefix != plan.Destination {
				return errors.New("npm global prefix changed after bootstrap. Refresh and confirm its actual destination before installing the harness.")
			}
		}
		if err := s.setJob(ctx, job.ID, "installing_harness", "Installing", "Installing "+recipe.Package+"@"+recipe.Version+" globally into "+n.Prefix+"."); err != nil {
			return err
		}
		if err := s.app.checkHarnessInstallState(ctx); err != nil {
			return err
		}
		mutationStarted = true
		if err := installGlobalPackage(ctx, n, recipe); err != nil {
			return err
		}
		if err := s.setJob(ctx, job.ID, "verifying", "Verifying", "Checking installed package metadata and the effective engine executable."); err != nil {
			return err
		}
		hs, bs = discoverWithNode(ctx, n)
		if err := ctx.Err(); err != nil {
			return err
		}
		verified := false
		for _, h := range hs {
			if h.ID == recipe.ID {
				if !h.Available || !h.Compatible || packageVersion(filepath.Join(n.Root, filepath.FromSlash(recipe.Package)), recipe.Package) != recipe.Version {
					return errors.New("npm finished, but the exact installed version/effective executable could not be verified. Resolve PATH/override conflicts and refresh before retrying.")
				}
				verified = true
			}
		}
		if !verified {
			return errors.New("Installed harness is missing from verification inventory.")
		}
		finalHarnesses, finalBinaries = hs, bs
		finalNode = n
		return nil
	}()
	if err != nil && mutationStarted {
		// Reconcile only failed/partial mutation. Success already has its single
		// final verification, and shutdown must never spawn fresh background probes.
		finalHarnesses, finalBinaries = nil, nil
		finalNode = nodeInstallation{}
		if s.app.checkHarnessInstallState(ctx) == nil {
			probeCtx, stop := context.WithTimeout(s.app.ctx, 15*time.Second)
			n := resolveNode(probeCtx)
			hs, bs := discoverWithNode(probeCtx, n)
			probeErr := probeCtx.Err()
			stop()
			if probeErr == nil {
				finalHarnesses, finalBinaries, finalNode = hs, bs, n
			} else {
				err = errors.Join(err, errors.New("Final reconciliation was incomplete. Availability remains unverified; refresh before retrying."))
			}
		}
	}
	if finalHarnesses == nil {
		finalHarnesses = s.app.harnessSnapshot()
		finalBinaries = map[string]binary{}
		for i := range finalHarnesses {
			finalHarnesses[i].Available = false
			finalHarnesses[i].Error = "Installation availability is unverified. Refresh Harnesses before using this executable."
		}
	}
	finalPlans := []harnessInstallPlan{}
	if finalNode.Node != "" {
		finalPlans = buildHarnessInstallPlans(finalNode, finalHarnesses, "")
	}
	s.finalize(ctx, job, finalHarnesses, finalBinaries, finalPlans, err, message, releaseLock)
}

// One public finalization boundary. Full refreshes cannot race it (probeMu),
// receipts cannot precede it (mu), and inventory/process admission cannot split
// it (app.mu). Cache work is done first, while admission is still reserved.
func (s *harnessInstallService) finalize(ctx context.Context, job harnessInstallJob, hs []Harness, bs map[string]binary, plans []harnessInstallPlan, installErr error, message string, releaseLock func()) {
	s.probeMu.Lock()
	s.app.invalidateHarnessCaches()
	s.mu.Lock()
	a := s.app
	a.mu.Lock()
	if a.storageErr != nil {
		// Keep the durable phase and admission reservation uncertain/frozen. A
		// usable writer does not authorize a terminal write after the latch.
		a.harnessInstallCancel = nil
		a.mu.Unlock()
		s.mu.Unlock()
		s.probeMu.Unlock()
		releaseLock()
		return
	}
	if ctx.Err() != nil || a.closing {
		job.State, job.Step = "interrupted", "Interrupted"
		installErr = errors.New("Installation was interrupted or exceeded its deadline. Global changes may be partial. Refresh and inspect before retrying.")
	} else if installErr != nil {
		job.State, job.Step = "failed", "Installation failed"
		var permission *installPermissionError
		if errors.As(installErr, &permission) || errors.Is(installErr, os.ErrPermission) {
			job.State, job.Step = "needs_permission", "Needs permission"
		}
	} else {
		job.State, job.Step = "completed", "Installed"
	}
	if installErr != nil {
		job.Error = installErr.Error()
		message = job.Error
	}
	job.UpdatedAt = now()
	for _, previous := range s.jobs {
		if previous.ID == job.ID {
			job.Log = append([]string{}, previous.Log...)
			break
		}
	}
	job.Log = append(job.Log, message)
	if len(job.Log) > 24 {
		job.Log = job.Log[len(job.Log)-24:]
	}
	var changes sqliteSessionChanges
	var err error
	pauseEnd := time.Now()
	paused := !a.harnessAdmissionStarted.IsZero()
	if !a.closing && a.ctx.Err() == nil {
		err = a.endHarnessAdmissionPauseLocked(pauseEnd)
	}
	if err == nil {
		changes, err = s.writeJobLocked(job, true)
	}
	if err != nil {
		// No completed receipt or usable inventory is exposed after failed
		// persistence. Startup will recover the unfinished durable job explicitly.
		if a.storageErr == nil {
			a.failStorageLocked(err)
		}
		a.harnessInstallCancel = nil
		a.mu.Unlock()
		s.mu.Unlock()
		s.probeMu.Unlock()
		releaseLock()
		return
	}
	if paused && !a.closing {
		// Terminal persistence also runs under the reservation. Retain that last
		// slice of timeout budget, without rereading or rewriting consultations.
		elapsed := time.Since(pauseEnd)
		for id, deadline := range a.harnessConsultationDeadlines {
			a.harnessConsultationDeadlines[id] = deadline.Add(elapsed)
		}
	}
	a.replaceHarnessInventoryLocked(hs, bs, true)
	for i := range s.jobs {
		if s.jobs[i].ID == job.ID {
			s.jobs[i] = job
			break
		}
	}
	s.plans = append([]harnessInstallPlan{}, plans...)
	s.inventoryReady = true
	releaseLock()
	a.harnessInstallCancel = nil
	a.harnessInstalling = false
	s.active = ""
	a.mu.Unlock()
	s.mu.Unlock()
	s.probeMu.Unlock()
	if a.sqlChanges != nil {
		a.sqlChanges.publish(changes)
	}
	a.resumeHarnessAdmissions()
}

// This runs only after explicit acceptance, before downloads/global changes.
// A real exclusive temporary create checks the existing OS token/ACL rather
// than guessing elevation from usernames or Unix mode bits. GET never does it.
func checkInstallWriteAccess(ctx context.Context, n nodeInstallation, recipe harnessRecipe) error {
	paths := []string{n.Prefix, filepath.Join(n.Root, filepath.FromSlash(recipe.Package))}
	if n.Bootstrap {
		paths = append(paths, filepath.Dir(n.Node), filepath.Dir(filepath.Dir(n.NPM)))
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := path
		for {
			info, err := os.Stat(parent)
			if err == nil {
				if !info.IsDir() {
					return errors.New("An installation destination is not a directory: " + parent)
				}
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return needsInstallPermission("Cannot access the global installation destination: " + parent + ". Grant the engine user appropriate OS directory access, then refresh.")
			}
			next := filepath.Dir(parent)
			if next == parent {
				return errors.New("Cannot resolve installation destination parent.")
			}
			parent = next
		}
		if seen[parent] {
			continue
		}
		seen[parent] = true
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.CreateTemp(parent, ".harness-permission-*")
		if err != nil {
			return needsInstallPermission("Global installation needs write access to " + parent + ". Have an administrator grant the engine user appropriate directory access or install the prerequisites, then refresh. No elevation was attempted.")
		}
		name := f.Name()
		if err := errors.Join(f.Close(), os.Remove(name)); err != nil {
			return errors.New("Could not clean up the temporary installation permission probe: " + name)
		}
	}
	return nil
}

func installGlobalPackage(ctx context.Context, n nodeInstallation, recipe harnessRecipe) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "klm-global-npm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// Neutral cwd excludes repository .npmrc and local lifecycle hooks. Keep the
	// user's normal home, prefix and proxy configuration; fixed flags own recipe.
	args := []string{n.NPM, "install", "--global", "--prefix", n.Prefix, "--registry=https://registry.npmjs.org/", "--@openai:registry=https://registry.npmjs.org/", "--@earendil-works:registry=https://registry.npmjs.org/", "--ignore-scripts=false", "--include=optional", "--bin-links=true", "--force=false", "--dry-run=false", "--no-audit", "--no-fund", "--loglevel=error", "--logs-max=0", recipe.Package + "@" + recipe.Version}
	cmd := exec.CommandContext(ctx, n.Node, args...)
	cmd.Dir = dir
	cmd.Env = runtimeEnvironment(os.Environ(), n.Node)
	var output, diagnostic cappedBuffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	owner, err := prepareOwnedProcess(cmd)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = owner.Close()
		}
	}()
	cmd.Cancel, cmd.WaitDelay = owner.Stop, 5*time.Second
	if err = cmd.Start(); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return needsInstallPermission("The engine user cannot execute npm or write its global destination. Grant the required OS access and retry.")
		}
		return errors.New("Could not start the global npm installer.")
	}
	if err = owner.Attach(); err != nil {
		_ = owner.Stop()
		_ = cmd.Wait()
		return err
	}
	err = cmd.Wait()
	closeErr := owner.Close()
	closed = true
	if closeErr != nil {
		return errors.New("Installer process shutdown could not be confirmed. Inspect the global installation before retrying.")
	}
	if err != nil {
		// Never publish raw npm output/config: registries, proxies and lifecycle
		// scripts may print secrets. Capture is bounded; only exit/known code leaves.
		text := diagnostic.String()
		if strings.Contains(text, "EACCES") || strings.Contains(text, "EPERM") {
			return needsInstallPermission("npm needs write access to the engine user's global prefix: " + n.Prefix + ". Have an administrator grant appropriate directory access or configure a normal user npm global prefix, then refresh.")
		}
		exit := -1
		if cmd.ProcessState != nil {
			exit = cmd.ProcessState.ExitCode()
		}
		return fmt.Errorf("Global npm installation failed (exit %d). Check engine network/proxy access and npm permissions, then refresh before retrying. Raw output is withheld because it may contain credentials.", exit)
	}
	return nil
}
