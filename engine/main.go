package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type turn struct {
	protocolScope         *protocolContentScope
	graphDelegation       *graphDelegationBinding
	delegation            *delegationExecution
	delegationChild       *delegationChildBinding
	automationRecoveryIDs []string
	analyticsID           string
	analyticsAnchor       time.Time           // Monotonic dispatch anchor; protected by app.mu.
	analyticsEndCandidate time.Time           // Native idle observed before final reconciliation.
	reasoningStart        *int                // Event boundary for this turn; protected by app.mu.
	steerWake             chan struct{}       // protected by app.mu
	steeringBatches       map[string][]string // Native request -> durable input IDs; protected by app.mu.
	restartMessageID      string              // Send now continuation after confirmed shutdown; protected by app.mu.
	graphNotificationID   string
	prompt                string // complete submission; graphConversationHooks prepends internal instructions
	consultationID        string
	delivery              bool
	ctx                   context.Context
	cancel                context.CancelFunc
	done                  chan struct{}
	stopErr               error // Written before done closes; read only after done.
	approvals             map[string]*pendingApproval
	questions             map[string]*pendingQuestion
	nativeYOLO            bool // Captured native policy; protected by app.mu.
	nativeYOLOConfigured  bool
}

type app struct {
	contentDownloads             contentDownloadTickets
	harnessInstallCancel         context.CancelFunc // app.mu; storage freeze cancels already-owned installer work.
	harnessInstaller             *harnessInstallService
	harnessInstalling            bool // app.mu: reserves admission before any global mutation.
	harnessAdmissionStarted      time.Time
	harnessConsultationDeadlines map[string]time.Time // app.mu; pauses queued expiry, never rewrites attributed creation times.
	harnessConsumers             atomic.Int64         // Admission increments under app.mu; release never takes app.mu while holding runtime.mu.
	store                        *sqliteStore         // Set only by the completed SQLite startup path.
	sqlChanges                   *sqliteChangeBus
	clientAuth                   *clientAuth
	restartEngine                func() error
	controlReady                 bool                 // Protected by mu; Linux local readiness waits for complete startup.
	restarting                   bool                 // Protected by mu; prevents duplicate restarts and settings writes during handoff.
	analyticsGraphClocks         map[string]time.Time // Monotonic clocks for owned runs; guarded by mu.
	activityBoardBusy            map[string]bool      // One scan or move per connection; guarded by mu.
	activityBoardSlots           chan struct{}

	libraryMu         sync.Mutex
	oauthMu           sync.Mutex
	credentialMu      sync.Mutex
	oauthTransactions map[string]*mcpOAuthTransaction
	githubActivity    githubActivityCache
	webhookWake       chan struct{}
	webhookIngress    chan struct{}
	webhookDeliveries map[webhookDeliveryKey]string
	webhookBytes      int64

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
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := validateEngineDataDir(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0700); err != nil {
		return errors.New("cannot create engine data directory")
	}
	lock, err := lockDataDir(filepath.Join(dir, "engine.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := prepareProtocolStaging(dir); err != nil {
		return err
	}
	// Reserve the fixed port before loading/recovering state or starting work.
	address, err := publicAPIListenAddress()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("cannot listen on port %s; it may be in use by another program: %w", apiPort, err)
	}
	defer listener.Close()
	return serveSQLiteEngine(dir, listener)
}
