package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Project struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Folder          string   `json:"folder"`
	Icon            string   `json:"icon"`
	Folders         []string `json:"folders"`
	ArchivedFolders []string `json:"archivedFolders"`
	Removed         bool     `json:"removed,omitempty"`
}

type Event struct {
	ConsultationID string         `json:"consultationId,omitempty"`
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Text           string         `json:"text"`
	Title          string         `json:"title,omitempty"`
	Status         string         `json:"status,omitempty"`
	Favorite       bool           `json:"favorite,omitempty"`
	CreatedAt      string         `json:"createdAt"`
	Data           map[string]any `json:"data,omitempty"`
}

type Session struct {
	Queue                []QueuedMessage         `json:"queue"`
	Role                 string                  `json:"role,omitempty"`
	GraphRunID           string                  `json:"graphRunId,omitempty"`
	GraphNodeID          string                  `json:"graphNodeId,omitempty"`
	ExecutionCWD         string                  `json:"executionCwd,omitempty"`
	SelectedGraphID      string                  `json:"selectedGraphId,omitempty"`
	SelectedAgent        *AgentRecord            `json:"selectedAgent,omitempty"`
	AgentSelectionLocked bool                    `json:"agentSelectionLocked,omitempty"`
	Graph                *ConversationGraphState `json:"graph,omitempty"` // HTTP/SSE view only
	ParentID             string                  `json:"parentId,omitempty"`
	Sources              []SourceReference       `json:"sources,omitempty"`
	ID                   string                  `json:"id"`
	ProjectID            string                  `json:"projectId"`
	Title                string                  `json:"title"`
	Workspace            string                  `json:"workspace"`
	Harness              string                  `json:"harness"`
	HarnessLocked        bool                    `json:"harnessLocked,omitempty"`
	YOLO                 bool                    `json:"yolo,omitempty"`
	Model                string                  `json:"model,omitempty"`
	Effort               string                  `json:"effort,omitempty"`
	ResolvedModel        string                  `json:"resolvedModel,omitempty"`
	ResolvedEffort       string                  `json:"resolvedEffort,omitempty"`
	Status               string                  `json:"status"`
	Archived             bool                    `json:"archived,omitempty"`
	RuntimeActive        bool                    `json:"runtimeActive,omitempty"` // HTTP/SSE view only
	Events               []Event                 `json:"events"`
	Permissions          []Permission            `json:"permissions,omitempty"`
	Questions            []QuestionRequest       `json:"questions,omitempty"`
	Usage                *SessionUsage           `json:"usage,omitempty"`
	CreatedAt            string                  `json:"createdAt"`
	UpdatedAt            string                  `json:"updatedAt"`
}

type nativeSession struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

const defaultTranscriptionModel = "gpt-4o-mini-transcribe"
const defaultOpenRouterTranscriptionModel = "google/gemini-2.5-flash"

type TranscriptionVocabularyEntry struct {
	Term string `json:"term"`
	Note string `json:"note"`
}

type TranscriptionSettings struct {
	Provider   string                         `json:"provider,omitempty"`
	OpenRouter TranscriptionProviderSettings  `json:"openrouter"`
	APIKey     string                         `json:"apiKey,omitempty"`
	Model      string                         `json:"model"`
	Language   string                         `json:"language,omitempty"`
	Vocabulary []TranscriptionVocabularyEntry `json:"vocabulary"`
}

type TranscriptionProviderSettings struct {
	APIKey string `json:"apiKey,omitempty"`
	Model  string `json:"model"`
}

type diskState struct {
	DeletedSessions     map[string]deletedSession `json:"deletedSessions,omitempty"`
	DeletedOperations   map[string]bool           `json:"deletedOperations,omitempty"`
	Images              map[string]storedImage    `json:"images,omitempty"`
	tx                  *stateTransaction
	JournalFormat       int                      `json:"journalFormat,omitempty"`
	AcceptedMessages    map[string]string        `json:"acceptedMessages,omitempty"`
	SessionSpawns       []SessionSpawn           `json:"sessionSpawns,omitempty"`
	SessionCommands     []SessionCommand         `json:"sessionCommands,omitempty"`
	QueuePayloads       map[string]queuedPayload `json:"queuePayloads,omitempty"`
	GraphRevision       uint64                   `json:"graphRevision"`
	GraphViewRevision   uint64                   `json:"graphViewRevision,omitempty"`
	GraphActivities     []GraphActivity          `json:"graphActivities,omitempty"`
	GraphRuns           []GraphRun               `json:"graphRuns,omitempty"`
	GraphActivations    []GraphActivation        `json:"graphActivations,omitempty"`
	GraphDeliveries     []GraphDelivery          `json:"graphDeliveries,omitempty"`
	GraphJoinRounds     []JoinRound              `json:"graphJoinRounds,omitempty"`
	GraphWorkspaces     []WorkspaceRecord        `json:"graphWorkspaces,omitempty"`
	GraphWorkspaceUses  []WorkspaceUse           `json:"graphWorkspaceUses,omitempty"`
	GraphNotifications  []RunNotification        `json:"graphNotifications,omitempty"`
	GraphCatalogChanges []GraphCatalogChange     `json:"graphCatalogChanges,omitempty"`
	Consultations       []Consultation           `json:"consultations,omitempty"`
	Version             int                      `json:"version"`
	Projects            []Project                `json:"projects"`
	Sessions            []Session                `json:"sessions"`
	Native              map[string]nativeSession `json:"native"`
	Grants              []permissionGrant        `json:"permissionGrants,omitempty"`
	Transcription       TranscriptionSettings    `json:"transcription"`
}

func normalizeTranscriptionSettings(settings *TranscriptionSettings) {
	if settings.Provider == "" {
		settings.Provider = "openai"
	}
	if settings.OpenRouter.Model == "" {
		settings.OpenRouter.Model = defaultOpenRouterTranscriptionModel
	}
	if settings.Model == "" {
		settings.Model = defaultTranscriptionModel
	}
	if settings.Vocabulary == nil {
		settings.Vocabulary = []TranscriptionVocabularyEntry{}
	}
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cannot generate secure identifier")
	}
	return hex.EncodeToString(b[:])
}

func event(kind, text string) Event {
	return Event{ID: newID(), Type: kind, Text: text, CreatedAt: now()}
}

func (d *diskState) session(id string) *Session {
	for i := range d.Sessions {
		if d.Sessions[i].ID == id {
			return &d.Sessions[i]
		}
	}
	return nil
}

func (d *diskState) project(id string) *Project {
	for i := range d.Projects {
		if d.Projects[i].ID == id {
			return &d.Projects[i]
		}
	}
	return nil
}

func loadState(dir string) (diskState, error) {
	d := diskState{Version: 2, Projects: []Project{}, Sessions: []Session{}, Native: map[string]nativeSession{},
		Transcription: TranscriptionSettings{Model: defaultTranscriptionModel, Vocabulary: []TranscriptionVocabularyEntry{}}}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	d = diskState{}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, fmt.Errorf("invalid state.json: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return d, errors.New("invalid trailing state.json content; refusing to overwrite")
	}
	if (d.Version != 1 && d.Version != 2) || d.Projects == nil || d.Sessions == nil || d.Native == nil {
		return d, errors.New("unsupported or incomplete state.json; refusing to overwrite")
	}
	if d.JournalFormat != 0 && d.JournalFormat != 1 {
		return d, errors.New("unsupported journal format in state.json")
	}
	if d.GraphViewRevision == 0 {
		d.GraphViewRevision = d.GraphRevision
	}
	normalizeTranscriptionSettings(&d.Transcription)
	ids := map[string]bool{}
	for i := range d.Projects {
		p := &d.Projects[i]
		if p.ID == "" || ids[p.ID] || p.Folders == nil {
			return d, errors.New("invalid project in state.json")
		}
		if p.ArchivedFolders == nil {
			p.ArchivedFolders = []string{}
		}
		archived := map[string]bool{}
		for _, name := range p.ArchivedFolders {
			found := false
			for _, folder := range p.Folders {
				if folder == name {
					found = true
					break
				}
			}
			key := strings.ToLower(name)
			if !found || archived[key] {
				return d, errors.New("invalid archived project folder in state.json")
			}
			archived[key] = true
		}
		ids[p.ID] = true
	}
	generalCount := 0
	for i := range d.Sessions {
		s := &d.Sessions[i]
		engineOwned := s.Role == sessionRoleGeneralAgent || s.Role == sessionRoleSubagent && s.ProjectID == "" && d.session(s.ParentID) != nil && d.session(s.ParentID).Role == sessionRoleGeneralAgent
		if s.Role == sessionRoleGeneralAgent {
			generalCount++
			if generalCount > 1 || s.ProjectID != "" || s.ParentID != "" || s.GraphRunID != "" || s.Archived || !filepath.IsAbs(s.ExecutionCWD) {
				return d, errors.New("invalid general conversation in state.json")
			}
			// Conservatively retain the lock for any accepted input, including an
			// input removed from the queue before execution or failed native turns.
			if len(s.Events) > 0 || len(s.Queue) > 0 {
				s.HarnessLocked = true
			}
			for key := range d.AcceptedMessages {
				if strings.HasPrefix(key, s.ID+"/") {
					s.HarnessLocked = true
					break
				}
			}
		}
		if s.ID == "" || ids[s.ID] || !engineOwned && d.project(s.ProjectID) == nil || s.Events == nil ||
			(s.Status != "idle" && s.Status != "running" && s.Status != "error") {
			return d, errors.New("invalid session in state.json")
		}
		ids[s.ID] = true
	}
	sideParents := map[string]bool{}
	for i := range d.Sessions {
		s := &d.Sessions[i]
		if s.ParentID != "" {
			// ParentID predates explicit child roles; existing children are side agents.
			if s.Role == "" {
				s.Role = sessionRoleSideAgent
			}
			parent := d.session(s.ParentID)
			if parent == nil || parent.ParentID != "" || parent.GraphRunID != "" || s.GraphRunID != "" || parent.ProjectID != s.ProjectID || (s.Role != sessionRoleSideAgent && s.Role != sessionRoleSubagent) {
				return d, errors.New("invalid linked conversation in state.json")
			}
			if s.Role == sessionRoleSideAgent {
				if sideParents[s.ParentID] {
					return d, errors.New("invalid linked conversation in state.json")
				}
				sideParents[s.ParentID] = true
			}
		}
	}
	for _, c := range d.Consultations {
		from, to := d.session(c.From), d.session(c.To)
		if c.ID == "" || ids[c.ID] || !consultationPair(from, to) || !validLinkedText(c.Question, 32<<10) {
			return d, errors.New("invalid consultation in state.json")
		}
		switch c.Status {
		case "queued", "answering", "completed", "failed", "cancelled", "interrupted":
		default:
			return d, errors.New("invalid consultation status in state.json")
		}
		switch c.Delivery {
		case "waiting", "pending", "delivering", "delivered", "failed", "cancelled", "interrupted":
		default:
			return d, errors.New("invalid consultation delivery in state.json")
		}
		if _, err := time.Parse(time.RFC3339Nano, c.CreatedAt); err != nil {
			return d, errors.New("invalid consultation timestamp in state.json")
		}
		ids[c.ID] = true
	}
	if err := validateSessionSpawns(&d); err != nil {
		return d, err
	}
	normalizeGraphWorkspaceRecords(&d)
	if err := validateGraphRecords(&d); err != nil {
		return d, err
	}
	if d.Version == 1 {
		d.Version = 2
		if err := saveState(dir, &d); err != nil {
			return d, fmt.Errorf("cannot migrate state.json to v2: %w", err)
		}
	}
	return d, nil
}

func saveState(dir string, d *diskState) error {
	if d.Version != 2 {
		return errors.New("refusing to write unsupported state version")
	}
	// Old binaries use DisallowUnknownFields and must refuse this storage rather
	// than start without replaying the transaction/possibly sealed journal.
	d.JournalFormat = 1
	normalizeGraphWorkspaceRecords(d)
	normalizeTranscriptionSettings(&d.Transcription)
	if err := validateGraphRecords(d); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(name, filepath.Join(dir, "state.json"))
}

// Caller holds app.mu. Every writer uses the same ordered durable transaction gate.
func (a *app) commitLocked(change func(*diskState)) error {
	return a.commitTransactionLocked(func(d *diskState) error { change(d); return nil })
}

func (a *app) failStorageLocked(err error) error {
	log.Printf("state persistence failed: %v", err)
	a.storageErr = errors.New("state persistence failed; engine is read-only until restarted")
	for _, r := range a.runs {
		r.cancel()
	}
	for _, r := range a.graphRuns {
		r.cancel()
	}
	return a.storageErr
}

func (a *app) notifyAllLocked() {
	for id, listeners := range a.listeners {
		if id == "*" {
			continue
		}
		for ch := range listeners {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	for subscription := range a.updateListeners {
		subscription.all = true
		select {
		case subscription.wake <- struct{}{}:
		default:
		}
	}
}
