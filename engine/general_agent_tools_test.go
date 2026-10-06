package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func generalControlFixture(t *testing.T) *app {
	t.Helper()
	a := &app{dir: t.TempDir(), ctx: context.Background(), binaries: map[string]binary{"pi": {}}, runs: map[string]*turn{"target": {ctx: context.Background()}}, state: diskState{
		Version: 2, Native: map[string]nativeSession{}, Projects: []Project{
			{ID: "p1", Name: "First", Folder: t.TempDir(), Folders: []string{}, ArchivedFolders: []string{}},
			{ID: "p2", Name: "Second", Folder: t.TempDir(), Folders: []string{}, ArchivedFolders: []string{}},
		}, Sessions: []Session{
			{ID: "general", Role: sessionRoleGeneralAgent, ExecutionCWD: t.TempDir(), Title: "General agent", Harness: "pi", Status: "idle", Events: []Event{}},
			{ID: "source", ProjectID: "p1", Title: "Source", Harness: "pi", Status: "idle", Events: []Event{}},
			{ID: "target", ProjectID: "p2", Title: "Target", Harness: "pi", Status: "idle", Events: []Event{}},
		},
	}}
	if err := saveState(a.dir, &a.state); err != nil {
		t.Fatal(err)
	}
	a.indexMessageIDsLocked()
	return a
}

func TestGeneralToolScopeAndAtomicSettings(t *testing.T) {
	contains := func(p *adapter, name string) bool {
		for _, tool := range p.bridgeTools() {
			if str(tool, "name") == name {
				return true
			}
		}
		return false
	}
	general := &adapter{role: sessionRoleGeneralAgent}
	if !contains(general, "project_list") || !contains(general, "session_send") || contains(general, "linked_discover") || contains(general, "session_spawn") || contains(general, "graph_invoke") {
		t.Fatal("general catalog has wrong capabilities")
	}
	if contains(&adapter{}, "project_list") || !contains(&adapter{}, "session_send") || contains(&adapter{role: sessionRoleSubagent}, "session_send") || contains(&adapter{role: "graph_node"}, "session_send") {
		t.Fatal("global or collaboration tools leaked to another role")
	}
	a := generalControlFixture(t)
	activeTurn := &turn{ctx: context.Background()}
	a.runs["general"], a.runs["source"] = activeTurn, activeTurn
	bridge := &linkedBridge{}
	bridge.bind(&adapter{app: a, id: "general", role: sessionRoleGeneralAgent, turn: activeTurn})
	if _, err := bridge.call(context.Background(), "project_list", []byte("{}")); err != nil {
		t.Fatal("project tool was misrouted as a graph tool", err)
	}
	bridge.bind(&adapter{app: a, id: "source", turn: activeTurn})
	if _, err := bridge.call(context.Background(), "session_discover", []byte("{}")); err != nil {
		t.Fatal("normal discovery cannot coexist with projectless general conversation", err)
	}
	delete(a.runs, "target")
	s := a.state.session("target")
	s.Model, s.Effort = "old", "high"
	a.catalogs = map[string]catalogCache{"p2/pi": {catalog: &ModelCatalog{Models: []ModelOption{{ID: "old", Efforts: []string{"high"}}, {ID: "new", Efforts: []string{"low"}}}}, expires: time.Now().Add(time.Hour)}}
	model, yolo := "new", true
	if err := a.changeSessionSettings(context.Background(), "target", &model, nil, &yolo); err == nil {
		t.Fatal("invalid preserved effort accepted")
	}
	s = a.state.session("target")
	if s.Model != "old" || s.Effort != "high" || s.YOLO {
		t.Fatal("invalid update was partially applied")
	}
	effort := "low"
	if err := a.changeSessionSettings(context.Background(), "target", &model, &effort, &yolo); err != nil {
		t.Fatal(err)
	}
	s = a.state.session("target")
	if s.Model != "new" || s.Effort != "low" || !s.YOLO {
		t.Fatal("valid atomic update not applied")
	}
	a.runs["target"] = &turn{ctx: context.Background()}
	yolo = false
	if err := a.changeSessionSettings(context.Background(), "target", &model, &effort, &yolo); err == nil || !a.state.session("target").YOLO {
		t.Fatal("busy destination model settings changed")
	}
}

func TestSessionCommandScopeAndRecovery(t *testing.T) {
	a := generalControlFixture(t)
	d := &a.state
	if commandPair(d, d.session("source"), d.session("target")) || commandPair(d, d.session("source"), d.session("general")) || commandPair(d, d.session("general"), d.session("general")) {
		t.Fatal("normal/global/self scope was widened")
	}
	if !commandPair(d, d.session("general"), d.session("target")) {
		t.Fatal("general agent cannot reach the project conversation")
	}
	if commandPair(d, d.session("general"), &Session{ID: "node", Role: "graph_node", GraphRunID: "run"}) {
		t.Fatal("graph node admitted as command destination")
	}
	args := sendArgs{SessionID: "target", Instruction: "Implement the requested change", OperationID: "send-1"}
	first, err := a.sendSessionLocked(d.session("general"), args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.sendSessionLocked(a.state.session("general"), args)
	if err != nil || first.(map[string]any)["messageId"] != second.(map[string]any)["messageId"] || len(a.state.session("target").Queue) != 1 {
		t.Fatal("retry duplicated the instruction", err)
	}
	args.Instruction = "Different work"
	if _, err := a.sendSessionLocked(a.state.session("general"), args); err == nil {
		t.Fatal("changed request reused an operation ID")
	}
	recovered := assertRecoveredTransaction(t, a)
	if len(recovered.SessionCommands) != 1 || recovered.session("target").Queue[0].Origin.Kind != "instruction" {
		t.Fatal("journal lost command receipt/origin")
	}
	q := recovered.session("target").Queue[0]
	appendQueuedUser(&recovered, "target", q)
	if e := recovered.session("target").Events[0]; e.Type != "agent_prompt" || e.ConsultationID != "" {
		t.Fatal("instruction became a user message or consultation")
	}
	if err := saveState(a.dir, &recovered); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadState(a.dir)
	if err != nil || len(reloaded.SessionCommands) != 1 || len(reloaded.session("target").Queue) != 0 {
		t.Fatal("receipt did not survive queue consumption/checkpoint", err)
	}
	a.state = reloaded
	args.Instruction = "Implement the requested change"
	if _, err := a.sendSessionLocked(a.state.session("general"), args); err != nil || len(a.state.session("target").Queue) != 0 {
		t.Fatal("reload retry re-enqueued work", err)
	}
}

func TestGeneralConsultationValidationAndRecovery(t *testing.T) {
	a := generalControlFixture(t)
	c := Consultation{ID: "consult", From: "general", To: "target", Topic: "Status", Question: "What is the current status?", Status: "queued", Delivery: "waiting", CreatedAt: now()}
	if err := a.commitLocked(func(d *diskState) {
		d.Consultations = append(d.Consultations, c)
		syncConsultation(d, &d.Consultations[0])
	}); err != nil {
		t.Fatal(err)
	}
	d := assertRecoveredTransaction(t, a)
	if err := saveState(a.dir, &d); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(a.dir); err != nil {
		t.Fatal("general consultation does not reload", err)
	}
	d.Consultations[0].From, d.Consultations[0].To = "target", "general"
	if err := validateGraphRecords(&d); err == nil {
		t.Fatal("normal conversation can initiate consultation to general")
	}
	d.Consultations[0].From, d.Consultations[0].To = "source", "target"
	if err := validateGraphRecords(&d); err == nil {
		t.Fatal("normal cross-project consultation admitted")
	}
}

func TestGeneralHistoryDeltaFragments(t *testing.T) {
	text := strings.Repeat("漢\x01", 14000)
	e := Event{ID: "partial", Type: "assistant", Text: text}
	a := &app{state: diskState{GraphRevision: 10}, journalBase: 5, historyJournal: map[string]*sessionJournal{"target": {floor: 5, records: []journalRecord{{revision: 6, changes: []EventChange{{Index: 0, Revision: 6, Event: e}}}}}}}
	s := &Session{ID: "target", Events: []Event{e}}
	since := uint64(5)
	args := generalToolArgs{SessionID: s.ID, SinceRevision: &since, Limit: 1}
	var assembled strings.Builder
	for pages := 0; pages < 100; pages++ {
		value, err := a.generalHistoryLocked("session_events_read", s, args)
		if err != nil {
			t.Fatal(err)
		}
		result := value.(map[string]any)
		encoded, _ := json.Marshal(result)
		if len(encoded) > agentToolByteLimit || !utf8.Valid(encoded) {
			t.Fatal("response violated byte/UTF-8 bounds")
		}
		item := result["items"].([]any)[0].(map[string]any)
		assembled.WriteString(item["fragment"].(string))
		cursor, more := result["nextCursor"].(string)
		if !more {
			if result["consumedRevision"] != uint64(10) {
				t.Fatal("completed delta did not advance revision")
			}
			break
		}
		if result["consumedRevision"] != since {
			t.Fatal("revision advanced before finishing the event")
		}
		args.Cursor = cursor
		// Later updates to the same event must not replace this cursor's version.
		if pages == 0 {
			a.state.GraphRevision = 11
			a.historyJournal[s.ID].records = append(a.historyJournal[s.ID].records, journalRecord{revision: 11, changes: []EventChange{{Index: 0, Revision: 11, Event: Event{ID: e.ID, Type: e.Type, Text: "updated"}}}})
		}
	}
	var got Event
	if json.Unmarshal([]byte(assembled.String()), &got) != nil || got.Text != text {
		t.Fatal("fragments mixed event versions or lost text")
	}
	args.Cursor = ""
	since = 10
	value, err := a.generalHistoryLocked("session_events_read", s, args)
	if err != nil || value.(map[string]any)["items"].([]any)[0].(map[string]any)["event"].(Event).Text != "updated" {
		t.Fatal("same event update missing from next delta", err)
	}
	since = 1
	value, err = a.generalHistoryLocked("session_events_read", s, args)
	if err != nil || value.(map[string]any)["resetRequired"] != true {
		t.Fatal("expired revision silently lost changes", err)
	}
}

func TestGeneralHistoryPaginationIgnoresOtherTurns(t *testing.T) {
	for _, tool := range []string{"session_messages_list", "session_events_read"} {
		t.Run(tool, func(t *testing.T) {
			a := &app{state: diskState{GraphRevision: 10}, journalBase: 5}
			s := &Session{ID: "target", UpdatedAt: now(), Events: []Event{{ID: "first", Type: "user", Text: "first"}, {ID: "second", Type: "assistant", Text: "second"}}}
			args := generalToolArgs{SessionID: s.ID, Limit: 1}
			if tool == "session_events_read" {
				since := uint64(1)
				args.SinceRevision = &since
			}
			first, err := a.generalHistoryLocked(tool, s, args)
			if err != nil {
				t.Fatal(err)
			}
			args.Cursor = first.(map[string]any)["nextCursor"].(string)
			a.state.GraphRevision++ // requester tool event; recipient history unchanged
			second, err := a.generalHistoryLocked(tool, s, args)
			if err != nil || second.(map[string]any)["items"].([]any)[0].(map[string]any)["id"] != "second" {
				t.Fatal("unrelated turn invalidated/restarted recipient pagination", err)
			}
			if second.(map[string]any)["revision"] != uint64(10) {
				t.Fatal("page mixed observed revisions")
			}
			s.UpdatedAt = now()
			s.Events[0].Text = "changed"
			a.appendHistoryRecordLocked(s.ID, 12, []EventChange{{Index: 0, Revision: 12, Event: s.Events[0]}}, false)
			if tool == "session_messages_list" {
				if _, err := a.generalHistoryLocked(tool, s, args); err == nil {
					t.Fatal("changed target history did not invalidate its cursor")
				}
			}
		})
	}
}
