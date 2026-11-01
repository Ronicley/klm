package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultEngineTimeZone = "America/Sao_Paulo"

type EngineSettings struct {
	TimeZone string `json:"timeZone"`
	Revision uint64 `json:"revision"`
}

type AutomationSchedule struct {
	Date       string `json:"date,omitempty"`
	Time       string `json:"time"`
	Recurrence string `json:"recurrence"`
	EveryDays  int    `json:"everyDays"`
	Weekdays   []int  `json:"weekdays"`
	MonthDay   int    `json:"monthDay"`
	Anchor     string `json:"anchor,omitempty"`
}

type Automation struct {
	AgentConfiguration *AgentExecutionCapture `json:"agentConfiguration,omitempty"` // Engine-owned run/recovery snapshot only.
	OnceTriggeredAt    string                 `json:"onceTriggeredAt,omitempty"`
	SessionMode        string                 `json:"sessionMode,omitempty"`
	SessionID          string                 `json:"sessionId,omitempty"`
	FollowUpPrompt     string                 `json:"followUpPrompt,omitempty"`
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"projectId"`
	Revision           uint64                 `json:"revision"`
	OperationID        string                 `json:"operationId"`
	CreationRequest    string                 `json:"creationRequest,omitempty"`
	Name               string                 `json:"name"`
	Prompt             string                 `json:"prompt"`
	Mode               string                 `json:"mode"`
	Enabled            bool                   `json:"enabled"`
	Parallel           bool                   `json:"parallel"`
	Harness            string                 `json:"harness"`
	Model              string                 `json:"model"`
	Effort             string                 `json:"effort"`
	YOLO               bool                   `json:"yolo"`
	Graphs             []string               `json:"graphs"`
	Selection          CapabilitySelection    `json:"selection"`
	TopicID            string                 `json:"topicId"`
	Schedule           AutomationSchedule     `json:"schedule"`
	NextScheduledAt    string                 `json:"nextScheduledAt,omitempty"`
	AfterSequence      uint64                 `json:"afterSequence"`
	Deleted            bool                   `json:"deleted,omitempty"`
}

type AutomationTrigger struct {
	BlockedReason string              `json:"blockedReason,omitempty"`
	ID            string              `json:"id"`
	AutomationID  string              `json:"automationId"`
	Key           string              `json:"key"`
	Source        string              `json:"source"`
	At            string              `json:"at"`
	Variables     map[string]string   `json:"variables,omitempty"`
	Snapshot      *Automation         `json:"snapshot,omitempty"`
	Capabilities  *CapabilitySnapshot `json:"capabilities,omitempty"`
	Consumed      bool                `json:"consumed"`
}

type AutomationRun struct {
	PromptKind        string              `json:"promptKind,omitempty"`
	SettingsResolved  bool                `json:"settingsResolved,omitempty"`
	ID                string              `json:"id"`
	AutomationID      string              `json:"automationId"`
	ProjectID         string              `json:"projectId"`
	SessionID         string              `json:"sessionId"`
	Snapshot          Automation          `json:"snapshot"`
	Trigger           AutomationTrigger   `json:"trigger"`
	Capabilities      *CapabilitySnapshot `json:"capabilities,omitempty"`
	Status            string              `json:"status"`
	Decision          string              `json:"decision,omitempty"`
	Reason            string              `json:"reason,omitempty"`
	Reminder          bool                `json:"reminder"`
	NeedsContinuation bool                `json:"needsContinuation"`
	StopRequested     bool                `json:"stopRequested"`
	StartedAt         string              `json:"startedAt"`
	FinishedAt        string              `json:"finishedAt,omitempty"`
}

type AutomationRecovery struct {
	RunID               string                         `json:"runId,omitempty"`
	TimeZone            string                         `json:"timeZone,omitempty"`
	NextOccurrenceAt    string                         `json:"nextOccurrenceAt,omitempty"`
	Resolved            bool                           `json:"resolved,omitempty"`
	OccurrenceDecisions []AutomationRecoveryOccurrence `json:"occurrenceDecisions,omitempty"`
	Resolutions         []AutomationRecoveryResolution `json:"resolutions,omitempty"`
	Capabilities        *CapabilitySnapshot            `json:"capabilities,omitempty"`
	ID                  string                         `json:"id"`
	Snapshot            Automation                     `json:"snapshot"`
	Variables           map[string]string              `json:"variables,omitempty"`
	Source              string                         `json:"source"`
	From                string                         `json:"from"`
	Until               string                         `json:"until"`
	SessionID           string                         `json:"sessionId,omitempty"`
	NoticeEventID       string                         `json:"noticeEventId,omitempty"`
	Delivery            string                         `json:"delivery"`
	Action              string                         `json:"action,omitempty"`
	OperationID         string                         `json:"operationId,omitempty"`
	UserEventID         string                         `json:"userEventId,omitempty"`
}

type AutomationRecoveryOccurrence struct {
	ID          string `json:"id"`
	ScheduledAt string `json:"scheduledAt"`
	Action      string `json:"action,omitempty"`
}

type AutomationRecoveryResolution struct {
	TimeZone      string   `json:"timeZone,omitempty"`
	OperationID   string   `json:"operationId"`
	Action        string   `json:"action"`
	UserEventID   string   `json:"userEventId"`
	OccurrenceIDs []string `json:"occurrenceIds"`
}

type AutomationSession struct {
	RunID        string `json:"runId"`
	AutomationID string `json:"automationId"`
	Status       string `json:"status"`
}

func (d *diskState) automation(id string) *Automation {
	for i := range d.Automations {
		if d.Automations[i].ID == id {
			return &d.Automations[i]
		}
	}
	return nil
}
func (d *diskState) automationRun(id string) *AutomationRun {
	for i := range d.AutomationRuns {
		if d.AutomationRuns[i].ID == id {
			return &d.AutomationRuns[i]
		}
	}
	return nil
}
func (d *diskState) activeAutomation(sessionID string) *AutomationRun {
	s := d.session(sessionID)
	if s == nil || s.Automation == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return nil
	}
	r := d.automationRun(s.Automation.RunID)
	if r != nil && r.SessionID == sessionID && r.Status == "running" {
		return r
	}
	return nil
}
func (d *diskState) engineZone() string {
	if d.EngineSettings.TimeZone == "" {
		return defaultEngineTimeZone
	}
	return d.EngineSettings.TimeZone
}

func (d *diskState) automationGraphAllowed(sessionID, graphID string) bool {
	if r := d.activeAutomation(sessionID); r != nil {
		return slices.Contains(r.Snapshot.Graphs, graphID)
	}
	return true
}

var automationReference = regexp.MustCompile(`\{\{[^{}]*\}\}`)
var automationVariables = []string{"{{pr.number}}", "{{organization.name}}", "{{repository.name}}"}

func validateAutomationConfig(v *Automation) error {
	name, ok := cleanLabel(v.Name, 200)
	if !ok {
		return errors.New("Name must contain 1 to 200 characters.")
	}
	v.Name = name
	sessionMode := automationSessionMode(*v)
	if sessionMode != "new_per_run" && sessionMode != "reuse_created" && sessionMode != "existing" {
		return errors.New("Choose a valid session mode.")
	}
	if sessionMode != "new_per_run" && v.Parallel {
		return errors.New("Reusable sessions cannot run in parallel.")
	}
	if sessionMode == "existing" && v.SessionID == "" {
		return errors.New("Choose an existing session.")
	}
	if sessionMode == "new_per_run" || sessionMode == "reuse_created" && v.SessionID == "" {
		if err := validateAutomationPrompt(v.Prompt, v.Mode); err != nil {
			return err
		}
	}
	if sessionMode != "new_per_run" {
		if err := validateAutomationPrompt(v.FollowUpPrompt, v.Mode); err != nil {
			return err
		}
	}
	if v.Mode != "webhook" && v.Mode != "schedule" {
		return errors.New("Choose webhook or schedule.")
	}
	if v.Mode == "schedule" {
		v.Parallel = false
		if err := validateAutomationSchedule(v.Schedule); err != nil {
			return err
		}
	}
	if !managedLibraryHarness(v.Harness) && !automationUsesSession(*v) {
		return errors.New("Choose Pi, OpenCode or Codex.")
	}
	if len(v.Model) > 200 || len(v.Effort) > 100 || strings.HasPrefix(v.Model, "-") {
		return errors.New("Invalid model or effort.")
	}
	if v.Harness == "opencode" && v.Model != "" && !strings.Contains(v.Model, "/") {
		return errors.New("OpenCode models require provider/model IDs.")
	}
	if len(v.Graphs) > 100 {
		return errors.New("Too many graphs selected.")
	}
	seen := map[string]bool{}
	for _, id := range v.Graphs {
		if !validAuthoringID(id) || seen[id] {
			return errors.New("Invalid graph selection.")
		}
		seen[id] = true
	}
	return normalizeSelection(&v.Selection)
}

func validateAutomationPrompt(prompt, mode string) error {
	if !validLinkedText(prompt, 128<<10) {
		return errors.New("Provide a task prompt of at most 128 KiB.")
	}
	for _, ref := range automationReference.FindAllString(prompt, -1) {
		if !slices.Contains(automationVariables, ref) {
			return fmt.Errorf("Unknown prompt reference: %s", ref)
		}
		if mode == "schedule" {
			return errors.New("Scheduled automations cannot use webhook references. Remove PR, organization and repository references from the task prompt.")
		}
	}
	remaining := automationReference.ReplaceAllString(prompt, "")
	if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
		return errors.New("Malformed prompt reference.")
	}
	return nil
}

func validateAutomationSchedule(s AutomationSchedule) error {
	if _, err := time.Parse("15:04", s.Time); err != nil {
		return errors.New("Choose a valid time.")
	}
	switch s.Recurrence {
	case "once":
		if _, err := time.Parse("2006-01-02", s.Date); err != nil {
			return errors.New("Choose a valid date for the one-time schedule.")
		}
	case "daily":
	case "every_days":
		if s.EveryDays < 1 || s.EveryDays > 365 {
			return errors.New("Every N days must be between 1 and 365.")
		}
	case "weekly":
		if len(s.Weekdays) == 0 || len(s.Weekdays) > 7 {
			return errors.New("Select weekdays.")
		}
		seen := map[int]bool{}
		for _, day := range s.Weekdays {
			if day < 0 || day > 6 || seen[day] {
				return errors.New("Invalid weekdays.")
			}
			seen[day] = true
		}
	case "monthly":
		if s.MonthDay < 1 || s.MonthDay > 31 {
			return errors.New("Choose a day from 1 to 31.")
		}
	default:
		return errors.New("Choose a recurrence.")
	}
	return nil
}

// Calendar arithmetic is deliberate: intervals are local dates, not 24-hour durations.
// Invalid DST wall times and months without the selected day are skipped.
func nextAutomationOccurrence(s AutomationSchedule, zone string, after time.Time) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}
	}
	clock, err := time.Parse("15:04", s.Time)
	if err != nil {
		return time.Time{}
	}
	if s.Recurrence == "once" {
		date, err := time.Parse("2006-01-02", s.Date)
		if err != nil {
			return time.Time{}
		}
		candidate := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if candidate.Format("2006-01-02") != s.Date || candidate.Hour() != clock.Hour() || candidate.Minute() != clock.Minute() || !candidate.After(after) {
			return time.Time{}
		}
		return candidate.UTC()
	}
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	anchor, _ := time.Parse("2006-01-02", s.Anchor)
	for n := 0; n < 366*9; n++ {
		d := day.AddDate(0, 0, n)
		match := s.Recurrence == "daily"
		switch s.Recurrence {
		case "weekly":
			match = slices.Contains(s.Weekdays, int(d.Weekday()))
		case "monthly":
			match = d.Day() == s.MonthDay
		case "every_days":
			date := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
			delta := int(date.Sub(anchor).Hours() / 24)
			match = s.EveryDays > 0 && delta >= 0 && delta%s.EveryDays == 0
		}
		if !match {
			continue
		}
		candidate := time.Date(d.Year(), d.Month(), d.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if candidate.Day() != d.Day() || candidate.Hour() != clock.Hour() || candidate.Minute() != clock.Minute() {
			continue
		}
		if candidate.After(after) {
			return candidate.UTC()
		}
	}
	return time.Time{}
}

func resetAutomationSchedule(v *Automation, zone string, at time.Time, resetAnchor bool) {
	v.NextScheduledAt = ""
	if v.Mode != "schedule" {
		return
	}
	if v.Schedule.Recurrence == "once" && v.OnceTriggeredAt != "" {
		return
	}
	if v.Schedule.Recurrence != "once" && (resetAnchor || v.Schedule.Anchor == "") {
		loc, _ := time.LoadLocation(zone)
		day := at.In(loc)
		clock, _ := time.Parse("15:04", v.Schedule.Time)
		if day.Hour()*60+day.Minute() >= clock.Hour()*60+clock.Minute() {
			day = day.AddDate(0, 0, 1)
		}
		v.Schedule.Anchor = day.Format("2006-01-02")
	}
	if v.Enabled {
		next := nextAutomationOccurrence(v.Schedule, zone, at)
		if !next.IsZero() {
			v.NextScheduledAt = next.Format(time.RFC3339Nano)
		}
	}
}

func enqueueAutomation(d *diskState, v Automation, key, source, at string, vars map[string]string, snapshot *Automation) string {
	for _, t := range d.AutomationTriggers {
		if t.AutomationID == v.ID && t.Key == key {
			return t.ID
		}
	}
	t := AutomationTrigger{ID: newID(), AutomationID: v.ID, Key: key, Source: source, At: at, Variables: vars, Snapshot: snapshot}
	d.AutomationTriggers = append(d.AutomationTriggers, t)
	return t.ID
}

func automationWebhook(d *diskState, e WebhookStoredEvent) {
	if e.Provider != "github" || e.EventType != "pull_request" || e.Action != "opened" {
		return
	}
	var payload struct {
		Number       int64 `json:"number"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"owner"`
		} `json:"repository"`
	}
	if json.Unmarshal([]byte(e.Payload), &payload) != nil || payload.Number <= 0 {
		return
	}
	org := payload.Organization.Login
	if org == "" && payload.Repository.Owner.Type == "Organization" {
		org = payload.Repository.Owner.Login
	}
	if len(org) > 200 || len(payload.Repository.Name) > 200 {
		return
	}
	if label, ok := cleanLabel(payload.Repository.Name, 200); !ok || label != payload.Repository.Name {
		return
	}
	if org != "" {
		if label, ok := cleanLabel(org, 200); !ok || label != org {
			return
		}
	}
	vars := map[string]string{"pr.number": strconv.FormatInt(payload.Number, 10), "organization.name": org, "repository.name": payload.Repository.Name}
	for _, v := range d.Automations {
		if !v.Deleted && v.Enabled && v.Mode == "webhook" && v.ProjectID == e.ProjectID && e.ReceivedSequence > v.AfterSequence {
			enqueueAutomation(d, v, "webhook/"+e.ID, "webhook", e.ReceivedAt, vars, nil)
		}
	}
}

func automationTask(r *AutomationRun) string {
	prompt := r.Snapshot.Prompt
	if r.PromptKind == "follow_up" {
		prompt = r.Snapshot.FollowUpPrompt
	}
	return automationReference.ReplaceAllStringFunc(prompt, func(ref string) string {
		return r.Trigger.Variables[strings.TrimSuffix(strings.TrimPrefix(ref, "{{"), "}}")]
	})
}
func automationPrompt(r *AutomationRun) string {
	return "Automation task (engine-authored, not a human authorization event):\n" + automationTask(r)
}

func finishAutomation(d *diskState, r *AutomationRun, status, reason string) {
	if r == nil || r.Status != "running" {
		return
	}
	r.Status, r.Reason, r.FinishedAt, r.NeedsContinuation = status, reason, now(), false
	if s := d.session(r.SessionID); s != nil && s.Automation != nil && s.Automation.RunID == r.ID {
		s.Status = "idle"
		if status == "execution_failed" {
			s.Status = "error"
		}
		s.Automation.Status = status
		pauseMessageQueue(s, "Automation finished. Send this message when ready.")
		e := event("status", "Automation: "+status+". "+reason)
		e.Data = map[string]any{"automationId": r.AutomationID, "runId": r.ID}
		s.Events = append(s.Events, e)
	}
}

func validateAutomationRecords(d *diskState) error {
	zone := d.engineZone()
	if zone == "Local" {
		return errors.New("Invalid engine time zone.")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, v := range d.Automations {
		copy := copyAutomation(v)
		if v.ID == "" || ids[v.ID] || d.project(v.ProjectID) == nil || !automationUsesSession(v) && d.project(v.ProjectID).topic(v.TopicID) == nil || v.Revision == 0 {
			return errors.New("Invalid automation record.")
		}
		ids[v.ID] = true
		if err := validateAutomationConfig(&copy); err != nil {
			return err
		}
		if v.NextScheduledAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, v.NextScheduledAt); err != nil || v.Mode != "schedule" || !v.Enabled {
				return errors.New("Invalid next automation occurrence.")
			}
		}
		if v.Schedule.Anchor != "" {
			if _, err := time.Parse("2006-01-02", v.Schedule.Anchor); err != nil {
				return errors.New("Invalid automation schedule anchor.")
			}
		}
	}
	triggerKeys := map[string]bool{}
	for _, trigger := range d.AutomationTriggers {
		key := trigger.AutomationID + "/" + trigger.Key
		if trigger.ID == "" || ids[trigger.ID] || triggerKeys[key] || d.automation(trigger.AutomationID) == nil {
			return errors.New("Invalid automation trigger.")
		}
		ids[trigger.ID] = true
		triggerKeys[key] = true
	}
	activeSessions := map[string]bool{}
	for _, r := range d.AutomationRuns {
		s := d.session(r.SessionID)
		if r.ID == "" || ids[r.ID] || d.automation(r.AutomationID) == nil || s == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" || s.ProjectID != r.ProjectID || r.Snapshot.ProjectID != r.ProjectID || r.Snapshot.ID != r.AutomationID {
			return errors.New("Invalid automation run/session association.")
		}
		ids[r.ID] = true
		if r.Status == "running" {
			if activeSessions[s.ID] || s.Automation == nil || s.Automation.RunID != r.ID {
				return errors.New("Invalid active automation session ownership.")
			}
			activeSessions[s.ID] = true
		}
		if r.PromptKind != "" && r.PromptKind != "initial" && r.PromptKind != "follow_up" {
			return errors.New("Invalid automation prompt kind.")
		}
		switch r.Status {
		case "running", "succeeded", "failed", "execution_failed", "interrupted_by_user":
		default:
			return errors.New("Invalid automation run status.")
		}
		if r.Decision != "" && r.Decision != "succeeded" && r.Decision != "failed" {
			return errors.New("Invalid automation outcome.")
		}
		if err := validateAutomationCapture(r.Capabilities, r.Snapshot.Harness); err != nil {
			return err
		}
	}
	for _, s := range d.Sessions {
		if s.Automation != nil {
			run := d.automationRun(s.Automation.RunID)
			if run == nil || run.SessionID != s.ID || run.Status != s.Automation.Status || run.AutomationID != s.Automation.AutomationID {
				return errors.New("Orphan automation session association.")
			}
		}
	}
	for _, r := range d.AutomationRecoveries {
		if r.ID == "" || ids[r.ID] || d.automation(r.Snapshot.ID) == nil {
			return errors.New("Invalid automation recovery.")
		}
		ids[r.ID] = true
		switch r.Delivery {
		case "pending", "delivering", "delivered", "uncertain":
		default:
			return errors.New("Invalid automation recovery delivery.")
		}
		if r.Action != "" && r.Action != "recover" && r.Action != "discard" {
			return errors.New("Invalid automation recovery action.")
		}
		if err := validateAutomationCapture(r.Capabilities, r.Snapshot.Harness); err != nil {
			return err
		}
		if err := validateRecoveryOccurrences(&r); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomationCapture(snapshot *CapabilitySnapshot, harness string) error {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	sealCapabilitySnapshot(&clone)
	if clone.Revision != snapshot.Revision || snapshot.Version != 1 || snapshot.Adapter != libraryAdapterID(harness) {
		return errors.New("Corrupted automation capability snapshot.")
	}
	return nil
}
