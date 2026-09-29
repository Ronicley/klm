package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type turn struct {
	steerWake           chan struct{} // protected by app.mu
	graphNotificationID string
	prompt              string // complete submission; graphConversationHooks prepends internal instructions
	consultationID      string
	delivery            bool
	ctx                 context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	stopErr             error // Written before done closes; read only after done.
	approvals           map[string]*pendingApproval
	questions           map[string]*pendingQuestion
}

type app struct {
	authoringMu     sync.Mutex
	mu              sync.Mutex
	checkpointMu    sync.Mutex
	messageIDs      map[string]bool
	graphValidation map[string]validatedGraphSnapshot
	dir             string
	state           diskState
	storageErr      error
	harnesses       []Harness
	binaries        map[string]binary
	runs            map[string]*turn
	stopVersions    map[string]uint64
	runtimes        map[string]*sessionRuntime
	graphRuns       map[string]*graphExecution // graph run ID -> lifetime independent of chat turns
	graphStarting   map[string]bool            // conversation reservations while capturing current definitions
	listeners       map[string]map[chan struct{}]bool
	updateListeners map[*updateSubscription]bool
	historyJournal  map[string]*sessionJournal
	streamIndexes   map[string]map[string]int
	journalBase     uint64
	picker          chan struct{}
	ctx             context.Context
	closing         bool
	wg              sync.WaitGroup
	catalogMu       sync.Mutex
	catalogs        map[string]catalogCache
	catalogFlights  map[string]*catalogFlight
	quotaMu         sync.Mutex
	quotas          map[string]quotaCache
}

func main() {
	if err := dispatchCLI(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func runEngine(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0700); err != nil {
		return errors.New("cannot create engine data directory")
	}
	lock, err := lockDataDir(filepath.Join(dir, "engine.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	// Reserve the fixed port before loading/recovering state or starting work.
	listener, err := net.Listen("tcp", "0.0.0.0:"+apiPort)
	if err != nil {
		return fmt.Errorf("cannot listen on port %s; it may be in use by another program: %w", apiPort, err)
	}
	defer listener.Close()
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	if err := replayStreamJournal(dir, &state); err != nil {
		return fmt.Errorf("cannot replay stream journal: %w", err)
	}
	if err := recoverGraphCatalogChanges(dir, &state); err != nil {
		return err
	}
	recoveryRevision := state.GraphRevision
	interrupted := interruptGraphState(&state, "Graph run interrupted by engine restart; execution was not replayed.")
	interruptedConsultations := map[string]string{}
	for i := range state.Consultations {
		c := &state.Consultations[i]
		if c.Status == "answering" {
			interruptedConsultations[c.To] = c.ID
		}
		if c.Delivery == "delivering" {
			interruptedConsultations[c.From] = c.ID
		}
		if !consultationTerminal(c.Status) || c.Delivery == "pending" || c.Delivery == "delivering" || c.Delivery == "waiting" {
			if !consultationTerminal(c.Status) {
				c.Status = "interrupted"
				c.Error = "Consultation interrupted by engine restart."
			}
			c.Delivery = "interrupted"
			syncConsultation(&state, c)
			interrupted = true
		}
	}
	for i := range state.Sessions {
		s := &state.Sessions[i]
		if len(s.Queue) > 0 {
			pauseMessageQueue(s, "Engine restarted. Send queued messages when ready.")
			interrupted = true
		}
		if len(s.Questions) > 0 {
			s.Questions = nil
			s.UpdatedAt = now()
			interrupted = true
		}
		if len(s.Permissions) > 0 {
			s.Permissions = nil
			s.UpdatedAt = now()
			interrupted = true
		}
		if s.Status == "running" {
			s.Status = "error"
			s.UpdatedAt = now()
			entry := event("error", "Execution interrupted by engine restart.")
			entry.ConsultationID = interruptedConsultations[s.ID]
			s.Events = append(s.Events, entry)
			if s.Role == sessionRoleSubagent {
				syncSubagentParent(&state, s, "interrupted")
			}
			interrupted = true
		}
	}
	if interrupted {
		if state.GraphRevision == recoveryRevision {
			state.GraphRevision++
		}
		state.GraphViewRevision = state.GraphRevision
		if err := saveState(dir, &state); err != nil {
			return errors.New("cannot persist interrupted sessions")
		}
	}
	// Fence downgrade before accepting the first transactional command. Existing
	// v1/v2 data and the previous stream format have already been read/replayed.
	if state.JournalFormat == 0 {
		if err := saveState(dir, &state); err != nil {
			return fmt.Errorf("cannot enable transaction journal: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	harnesses, binaries := discoverHarnesses()
	a := &app{dir: dir, state: state, harnesses: harnesses, binaries: binaries,
		runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}, graphRuns: map[string]*graphExecution{}, listeners: map[string]map[chan struct{}]bool{},
		historyJournal: map[string]*sessionJournal{}, journalBase: state.GraphRevision, picker: make(chan struct{}, 1), ctx: ctx}
	a.indexMessageIDsLocked()
	// Pay snapshot validation/compilation once at startup, before serving commands.
	a.graphValidation = map[string]validatedGraphSnapshot{}
	if err := validateGraphRecordsCached(&a.state, a.graphValidation); err != nil {
		return err
	}
	go a.expireConsultations()
	a.mu.Lock()
	a.scheduleLinkedLocked()
	a.mu.Unlock()
	shutdown := make(chan struct{})
	control, err := serveEngineControl(dir, a, shutdown)
	if err != nil {
		return err
	}
	defer control.Close()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.checkpointStreamJournal() }()
	server := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	log.Printf("KLM engine listening on http://%s", listener.Addr())
	select {
	case <-stopped:
	case <-shutdown:
	case err = <-serveErr:
	}
	a.mu.Lock()
	a.closing = true
	cancel()
	a.mu.Unlock()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	_ = server.Close()
	a.wg.Wait()
	a.shutdownSessionRuntimes()
	a.mu.Lock()
	if a.storageErr == nil && len(a.state.GraphRuns) > 0 {
		if persistErr := a.commitLocked(func(d *diskState) {
			interruptGraphState(d, "Graph run interrupted by engine shutdown; execution will not resume.")
		}); persistErr != nil && err == nil {
			err = persistErr
		}
	}
	a.mu.Unlock()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
