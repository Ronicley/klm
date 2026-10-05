package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

const (
	historyPageLimit        = 50
	historyJournalChanges   = 256
	historyJournalByteLimit = 4 << 20
)

type SessionSummary struct {
	Queue           []QueuedMessage         `json:"queue"`
	Role            string                  `json:"role,omitempty"`
	GraphRunID      string                  `json:"graphRunId,omitempty"`
	GraphNodeID     string                  `json:"graphNodeId,omitempty"`
	ExecutionCWD    string                  `json:"executionCwd,omitempty"`
	SelectedGraphID string                  `json:"selectedGraphId,omitempty"`
	Graph           *ConversationGraphState `json:"graph,omitempty"`
	ParentID        string                  `json:"parentId,omitempty"`
	ID              string                  `json:"id"`
	ProjectID       string                  `json:"projectId"`
	Title           string                  `json:"title"`
	Workspace       string                  `json:"workspace"`
	Harness         string                  `json:"harness"`
	HarnessLocked   bool                    `json:"harnessLocked,omitempty"`
	YOLO            bool                    `json:"yolo,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Effort          string                  `json:"effort,omitempty"`
	ResolvedModel   string                  `json:"resolvedModel,omitempty"`
	ResolvedEffort  string                  `json:"resolvedEffort,omitempty"`
	Status          string                  `json:"status"`
	Archived        bool                    `json:"archived,omitempty"`
	RuntimeActive   bool                    `json:"runtimeActive,omitempty"`
	Permissions     []Permission            `json:"permissions,omitempty"`
	Questions       []QuestionRequest       `json:"questions,omitempty"`
	Usage           *SessionUsage           `json:"usage,omitempty"`
	CreatedAt       string                  `json:"createdAt"`
	UpdatedAt       string                  `json:"updatedAt"`
}

type EventPage struct {
	SessionID  string  `json:"sessionId"`
	Revision   uint64  `json:"revision"`
	StartIndex int     `json:"startIndex"`
	EndIndex   int     `json:"endIndex"`
	Total      int     `json:"total"`
	Events     []Event `json:"events"`
	NextCursor string  `json:"nextCursor,omitempty"`
	HasMore    bool    `json:"hasMore"`
}

type EventChange struct {
	Index    int    `json:"index"`
	Revision uint64 `json:"revision"`
	Event    Event  `json:"event"`
}

type SessionUpdate struct {
	Kind     string         `json:"kind"`
	Revision uint64         `json:"revision"`
	Summary  SessionSummary `json:"summary"`
	Total    int            `json:"total"`
	Changes  []EventChange  `json:"changes"`
	History  *EventPage     `json:"history,omitempty"`
}

type journalRecord struct {
	revision   uint64
	changes    []EventChange
	forceReset bool
	bytes      int
}

type sessionJournal struct {
	floor       uint64
	records     []journalRecord
	changeCount int
	bytes       int
}

func sessionSummary(view *Session) SessionSummary {
	return SessionSummary{
		Queue: view.Queue,
		Role:  view.Role, GraphRunID: view.GraphRunID, GraphNodeID: view.GraphNodeID, ExecutionCWD: view.ExecutionCWD,
		SelectedGraphID: view.SelectedGraphID, Graph: view.Graph, ParentID: view.ParentID, ID: view.ID, ProjectID: view.ProjectID,
		Title: view.Title, Workspace: view.Workspace, Harness: view.Harness, HarnessLocked: view.HarnessLocked, YOLO: view.YOLO, Model: view.Model, Effort: view.Effort,
		ResolvedModel: view.ResolvedModel, ResolvedEffort: view.ResolvedEffort, Status: view.Status, Archived: view.Archived, RuntimeActive: view.RuntimeActive,
		Permissions: view.Permissions, Questions: view.Questions, Usage: view.Usage, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}
}

func (a *app) sessionSummaryLocked(id string) *SessionSummary {
	view := a.sessionViewLocked(id)
	if view == nil {
		return nil
	}
	summary := sessionSummary(view)
	return &summary
}

func historyWorkEvent(event Event) bool {
	switch event.Type {
	case "reasoning", "command", "mcp", "tool", "subagent", "status":
		return true
	default:
		return false
	}
}

func consultationKey(event Event) string {
	if event.ConsultationID != "" {
		return event.ConsultationID
	}
	if event.Type == "consultation" && event.Data != nil {
		if id, ok := event.Data["requestId"].(string); ok {
			return id
		}
	}
	return ""
}

func historyStart(events []Event, end, limit int) int {
	start := max(0, end-limit)
	for {
		previous := start
		if start > 0 && historyWorkEvent(events[start]) && historyWorkEvent(events[start-1]) {
			for start > 0 && historyWorkEvent(events[start-1]) {
				start--
			}
		}
		groups := map[string]bool{}
		for _, entry := range events[start:end] {
			if key := consultationKey(entry); key != "" {
				groups[key] = true
			}
		}
		for i := 0; i < start; i++ {
			if groups[consultationKey(events[i])] {
				start = i
				break
			}
		}
		if start == previous {
			return start
		}
	}
}

func eventPage(sessionID string, revision uint64, events []Event, end, limit int) EventPage {
	start := historyStart(events, end, limit)
	pageEvents := append([]Event(nil), events[start:end]...)
	page := EventPage{SessionID: sessionID, Revision: revision, StartIndex: start, EndIndex: end, Total: len(events), Events: pageEvents, HasMore: start > 0}
	if page.HasMore && len(pageEvents) > 0 {
		page.NextCursor = pageEvents[0].ID
	}
	return page
}

func (a *app) historyPageLocked(id, cursor string, limit int) (*EventPage, bool) {
	s := a.state.session(id)
	if s == nil {
		return nil, false
	}
	end := len(s.Events)
	if cursor != "" {
		end = -1
		for i := range s.Events {
			if s.Events[i].ID == cursor {
				end = i
				break
			}
		}
		if end < 0 {
			return nil, true
		}
	}
	page := eventPage(id, a.state.GraphRevision, s.Events, end, limit)
	return &page, false
}

func (a *app) history(w http.ResponseWriter, r *http.Request) {
	limit := historyPageLimit
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > historyPageLimit {
			fail(w, 400, "History limit must be between 1 and 50.")
			return
		}
		limit = parsed
	}
	a.mu.Lock()
	page, conflict := a.historyPageLocked(r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
	a.mu.Unlock()
	if page == nil {
		if conflict {
			fail(w, http.StatusConflict, "History cursor is no longer available. Reload the latest history.")
		} else {
			fail(w, 404, "Session not found.")
		}
		return
	}
	respond(w, 200, page)
}

func (a *app) exportSession(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	view := a.sessionViewLocked(r.PathValue("id"))
	if view != nil {
		view.Events = append([]Event{}, view.Events...)
	}
	a.mu.Unlock()
	if view == nil {
		fail(w, 404, "Session not found.")
		return
	}
	respond(w, 200, view)
}

func changedEvents(before, after []Event, revision uint64) ([]EventChange, bool) {
	if len(after) < len(before) {
		return nil, true
	}
	changes := []EventChange{}
	for i := range before {
		if before[i].ID != after[i].ID {
			return nil, true
		}
		if !reflect.DeepEqual(before[i], after[i]) {
			changes = append(changes, EventChange{Index: i, Revision: revision, Event: after[i]})
		}
	}
	for i := len(before); i < len(after); i++ {
		changes = append(changes, EventChange{Index: i, Revision: revision, Event: after[i]})
	}
	return changes, false
}

func (a *app) recordHistoryLocked(before diskState) {
	if a.historyJournal == nil {
		a.historyJournal = map[string]*sessionJournal{}
	}
	revision := a.state.GraphRevision
	beforeSessions := make(map[string]Session, len(before.Sessions))
	for _, session := range before.Sessions {
		beforeSessions[session.ID] = session
	}
	for _, session := range a.state.Sessions {
		previous, existed := beforeSessions[session.ID]
		if !existed {
			continue
		}
		// All event writers bump the owning session timestamp. Do not compare
		// histories of unrelated sessions for ordinary metadata commits.
		if previous.UpdatedAt == session.UpdatedAt && len(previous.Events) == len(session.Events) {
			continue
		}
		changes, reset := changedEvents(previous.Events, session.Events, revision)
		if len(changes) == 0 && !reset {
			continue
		}
		a.appendHistoryRecordLocked(session.ID, revision, changes, reset)
	}
}

func (a *app) appendHistoryChangesLocked(id string, revision uint64, changes []EventChange) {
	a.appendHistoryRecordLocked(id, revision, changes, false)
}

func (a *app) appendHistoryRecordLocked(id string, revision uint64, changes []EventChange, reset bool) {
	if a.historyJournal == nil {
		a.historyJournal = map[string]*sessionJournal{}
	}
	encoded, _ := json.Marshal(changes)
	if len(changes) > historyJournalChanges || len(encoded) > historyJournalByteLimit {
		changes, reset = nil, true
	}
	journal := a.historyJournal[id]
	if journal == nil {
		journal = &sessionJournal{floor: a.journalBase}
		a.historyJournal[id] = journal
	}
	record := journalRecord{revision: revision, changes: changes, forceReset: reset, bytes: len(encoded)}
	journal.records = append(journal.records, record)
	journal.changeCount += len(changes)
	journal.bytes += record.bytes
	for len(journal.records) > 0 && (journal.changeCount > historyJournalChanges || journal.bytes > historyJournalByteLimit) {
		removed := journal.records[0]
		journal.records = journal.records[1:]
		journal.floor = removed.revision
		journal.changeCount -= len(removed.changes)
		journal.bytes -= removed.bytes
	}
}

func (a *app) sessionUpdateLocked(id string, since uint64, initial bool) *SessionUpdate {
	summary := a.sessionSummaryLocked(id)
	s := a.state.session(id)
	if summary == nil || s == nil {
		return nil
	}
	revision := a.state.GraphRevision
	update := &SessionUpdate{Kind: "delta", Revision: revision, Summary: *summary, Total: len(s.Events), Changes: []EventChange{}}
	journal := a.historyJournal[id]
	floor := a.journalBase
	if journal != nil {
		floor = journal.floor
	}
	reset := initial || since > revision || since < floor
	if !reset && journal != nil {
		for _, record := range journal.records {
			if record.revision <= since {
				continue
			}
			if record.forceReset {
				reset = true
				break
			}
			update.Changes = append(update.Changes, record.changes...)
		}
	}
	encoded, _ := json.Marshal(update.Changes)
	if len(update.Changes) > historyJournalChanges || len(encoded) > historyJournalByteLimit {
		reset = true
	}
	if reset {
		page := eventPage(id, revision, s.Events, len(s.Events), historyPageLimit)
		update.Kind, update.Changes, update.History = "reset", []EventChange{}, &page
	}
	return update
}

func (a *app) currentSessionUpdateLocked(id string) *SessionUpdate {
	revision := a.state.GraphRevision
	update := a.sessionUpdateLocked(id, revision, false)
	if update == nil {
		return nil
	}
	journal := a.historyJournal[id]
	if journal == nil {
		return update
	}
	for _, record := range journal.records {
		if record.revision != revision {
			continue
		}
		if record.forceReset {
			page := eventPage(id, revision, a.state.session(id).Events, update.Total, historyPageLimit)
			update.Kind, update.History = "reset", &page
			return update
		}
		update.Changes = append(update.Changes, record.changes...)
	}
	return update
}

func parseEventRevision(r *http.Request) (uint64, bool) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if query := strings.TrimSpace(r.URL.Query().Get("since")); query != "" {
		value = query
	}
	if value == "" {
		return 0, false
	}
	revision, err := strconv.ParseUint(value, 10, 64)
	return revision, err == nil
}
