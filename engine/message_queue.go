package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Prepared attachments are stored separately from the public queue projection.
type QueuedMessage struct {
	Origin   *SpawnOrigin      `json:"origin,omitempty"`
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Status   string            `json:"status"`
	Mode     string            `json:"mode"`
	Error    string            `json:"error,omitempty"`
	Sources  []SourceReference `json:"sources,omitempty"`
	Mentions []Mention         `json:"mentions,omitempty"`
}
type queuedPayload struct {
	Submission submission
	Harness    string
	Directory  string
}

// Preserve message boundaries and each message's prepared attachments in one
// native submission. The public timeline still retains every original input.
func queuedSubmission(d *diskState, messages []QueuedMessage) (submission, error) {
	inputs := make([]submission, 0, len(messages))
	for _, q := range messages {
		payload, ok := d.QueuePayloads[q.ID]
		if !ok {
			return submission{}, errors.New("Queued message content is unavailable.")
		}
		inputs = append(inputs, payload.Submission)
	}
	return combineSubmissions(inputs), nil
}

func combineSubmissions(inputs []submission) submission {
	if len(inputs) == 1 {
		return inputs[0]
	}
	var combined submission
	for i, input := range inputs {
		if i > 0 {
			combined.Text += "\n\n"
		}
		combined.Text += fmt.Sprintf("Message %d:\n%s", i+1, input.piText())
		combined.Files = append(combined.Files, input.Files...)
	}
	return combined
}

func steeringMessages(s *Session) []QueuedMessage {
	var messages []QueuedMessage
	for _, q := range s.Queue {
		if q.Status == "steering" {
			messages = append(messages, q)
		}
	}
	return messages
}

// Persist the selected input before cancelling anything. Other unsent inputs
// remain recoverable but do not race the replacement prompt into the model.
func prepareMessageInterrupt(d *diskState, id, messageID string, running bool) {
	s := d.session(id)
	pauseMessageQueue(s, "Execution interrupted by Send now. Send this message when ready.")
	for i, q := range s.Queue {
		if q.ID != messageID {
			continue
		}
		q.Mode, q.Status, q.Error = "interrupt", "queued", ""
		if running {
			q.Status = "interrupting"
		}
		s.Queue = append([]QueuedMessage{q}, append(s.Queue[:i], s.Queue[i+1:]...)...)
		return
	}
}

// Called only after durable acceptance, under app.mu. execute releases the old
// reservation and starts the selected input only after owned shutdown succeeds.
func (a *app) interruptForMessageLocked(id, messageID string) {
	if a.stopVersions == nil {
		a.stopVersions = map[string]uint64{}
	}
	a.stopVersions[id]++
	if t := a.runs[id]; t != nil {
		t.restartMessageID = messageID
		t.cancel()
	}
}

func removeQueuedMessage(d *diskState, sessionID, messageID string) {
	s := d.session(sessionID)
	for i, q := range s.Queue {
		if q.ID == messageID {
			s.Queue = append(s.Queue[:i], s.Queue[i+1:]...)
			break
		}
	}
	delete(d.QueuePayloads, messageID)
	s.UpdatedAt = now()
}

func appendQueuedUser(d *diskState, sessionID string, q QueuedMessage) {
	s := d.session(sessionID)
	payload := d.QueuePayloads[q.ID].Submission
	kind := "user"
	if q.Origin != nil {
		kind = "agent_prompt"
	}
	e := event(kind, q.Text)
	e.ID = q.ID
	e.Data = map[string]any{"delivery": q.Mode}
	if q.Origin != nil {
		e.Data["origin"] = q.Origin
	}
	if len(q.Sources) > 0 {
		e.Data["sources"] = q.Sources
		s.Sources = append(s.Sources, q.Sources...)
	}
	if len(q.Mentions) > 0 {
		e.Data["mentions"] = q.Mentions
		e.Data["mentionPreparation"] = payload.Preparation
	}
	s.Events = append(s.Events, e)
	removeQueuedMessage(d, sessionID, q.ID)
}

// Never replay an input whose native acknowledgement was lost.
func pauseMessageQueue(s *Session, reason string) {
	s.UpdatedAt = now()
	for i := range s.Queue {
		q := &s.Queue[i]
		switch q.Status {
		case "sending", "accepted":
			q.Status, q.Error = "uncertain", "Delivery was not confirmed. Check the conversation before sending again."
		case "queued", "steering", "interrupting":
			q.Status, q.Error = "paused", reason
		}
	}
}

func (a *app) wakeSteeringLocked(id string) {
	if t := a.runs[id]; t != nil && t.steerWake != nil {
		select {
		case t.steerWake <- struct{}{}:
		default:
		}
	}
}

func (p *adapter) steeringChannel() <-chan struct{} {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.graphNode() {
		return nil
	}
	if p.turn.steerWake == nil {
		p.turn.steerWake = make(chan struct{}, 1)
	}
	p.app.wakeSteeringLocked(p.id)
	return p.turn.steerWake
}

// Claim every currently pending steering input as one native submission. A
// native ACK releases transport serialization, not necessarily consumption.
func (p *adapter) takeSteering() (*QueuedMessage, submission, error) {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || p.turn.ctx.Err() != nil || a.runs[p.id] != nil && a.runs[p.id] != p.turn {
		return nil, submission{}, nil
	}
	s := a.state.session(p.id)
	for _, q := range s.Queue {
		if q.Status == "sending" {
			return nil, submission{}, nil
		}
	}
	messages := steeringMessages(s)
	if len(messages) == 0 {
		return nil, submission{}, nil
	}
	payload, err := queuedSubmission(&a.state, messages)
	if err != nil {
		return nil, submission{}, err
	}
	// A literal prefix avoids Pi command/template expansion and provides an
	// exact native user-message correlation even for repeated identical text.
	if p.harness == "pi" {
		payload.Text = "Steering input " + messages[0].ID + ":\n" + payload.Text
	}
	if err := validateSubmissionSize(payload, p.harness); err != nil {
		return nil, submission{}, err
	}
	ids := make([]string, 0, len(messages))
	for _, q := range messages {
		ids = append(ids, q.ID)
	}
	if err := a.commitLocked(func(d *diskState) {
		next := d.session(p.id)
		for i := range next.Queue {
			if next.Queue[i].Status == "steering" {
				next.Queue[i].Status = "sending"
			}
		}
		next.UpdatedAt = now()
	}); err != nil {
		return nil, submission{}, err
	}
	if p.turn.steeringBatches == nil {
		p.turn.steeringBatches = map[string][]string{}
	}
	p.turn.steeringBatches[messages[0].ID] = ids
	return &messages[0], payload, nil
}

// Pi and Codex acknowledge volatile native queues before user incorporation.
// Retain the durable payload until a correlated native user message arrives.
func (p *adapter) acknowledgeSteering(id string) error {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.commitLocked(func(d *diskState) {
		for i := range d.session(p.id).Queue {
			q := &d.session(p.id).Queue[i]
			for _, inputID := range p.turn.steeringBatches[id] {
				if q.ID == inputID && q.Status == "sending" {
					q.Status = "accepted"
				}
			}
		}
	})
	a.wakeSteeringLocked(p.id)
	return err
}

// Private native hooks claim exactly one batch at their model boundary. They
// are not model tools and cannot steer another native session or a graph node.
func (p *adapter) steeringBoundary(method string, raw json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"sessionID"`
		BatchID   string `json:"batchID"`
	}
	if json.Unmarshal(raw, &params) != nil {
		return nil, errors.New("Invalid steering boundary.")
	}
	if p.harness == "opencode" {
		p.app.mu.Lock()
		nativeID := p.app.state.Native[p.id].ID
		p.app.mu.Unlock()
		if params.SessionID == "" || params.SessionID != nativeID {
			return map[string]any{}, nil // Native child sessions never consume parent input.
		}
	}
	if method == "klm/steering/consumed" {
		p.app.mu.Lock()
		known := len(p.turn.steeringBatches[params.BatchID]) > 0
		p.app.mu.Unlock()
		if !known {
			return nil, errors.New("Unknown steering batch.")
		}
		return map[string]any{"consumed": true}, p.finishSteering(params.BatchID, "")
	}
	q, payload, err := p.takeSteering()
	if err != nil || q == nil {
		return map[string]any{}, err
	}
	if p.harness == "opencode" {
		return map[string]any{"batchID": q.ID, "parts": payload.openCodeParts()}, nil
	}
	return map[string]any{"batchID": q.ID, "text": payload.piText()}, nil
}

func (p *adapter) finishPiSteering(text string) error {
	p.app.mu.Lock()
	match := ""
	for id, ids := range p.turn.steeringBatches {
		var messages []QueuedMessage
		for _, inputID := range ids {
			messages = append(messages, QueuedMessage{ID: inputID})
		}
		payload, err := queuedSubmission(&p.app.state, messages)
		if err == nil {
			payload.Text = "Steering input " + id + ":\n" + payload.Text
			if payload.piText() == text {
				match = id
				break
			}
		}
	}
	p.app.mu.Unlock()
	if match != "" {
		return p.finishSteering(match, "")
	}
	return nil
}

func (p *adapter) hasUnconfirmedSteering() bool {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	return len(p.turn.steeringBatches) > 0
}

func (p *adapter) finishSteering(id, failure string) error {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := p.turn.steeringBatches[id]
	if len(ids) == 0 {
		ids = []string{id}
	}
	var found []QueuedMessage
	for _, q := range a.state.session(p.id).Queue {
		for _, inputID := range ids {
			if q.ID == inputID && (q.Status == "sending" || q.Status == "accepted" || q.Status == "uncertain") {
				found = append(found, q)
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	err := a.commitLocked(func(d *diskState) {
		for _, q := range found {
			if failure == "" {
				appendQueuedUser(d, p.id, q)
				continue
			}
			for i := range d.session(p.id).Queue {
				if d.session(p.id).Queue[i].ID == q.ID {
					d.session(p.id).Queue[i].Status, d.session(p.id).Queue[i].Error = "paused", failure
				}
			}
		}
		d.session(p.id).UpdatedAt = now()
	})
	if err == nil {
		delete(p.turn.steeringBatches, id)
	}
	a.wakeSteeringLocked(p.id)
	return err
}

// Called under app.mu, sharing the same one-turn reservation as ordinary sends.
func (a *app) scheduleMessagesLocked() {
	if a.closing || a.storageErr != nil {
		return
	}
	for _, s := range a.state.Sessions {
		if a.runs[s.ID] != nil || len(s.Queue) == 0 {
			continue
		}
		messages := steeringMessages(&s)
		// An interrupt replacement is exclusive. Steering otherwise takes
		// priority over ordinary follow-ups, including a blocked queue head.
		if s.Queue[0].Mode == "interrupt" && s.Queue[0].Status == "queued" || len(messages) == 0 {
			if s.Queue[0].Status != "queued" {
				continue
			}
			messages = []QueuedMessage{s.Queue[0]}
		}
		q := messages[0]
		folder, available := a.state.conversationDirectory(&s)
		payload, found := a.state.QueuePayloads[q.ID]
		b, installed := a.binaries[s.Harness]
		for _, input := range messages {
			prepared, ok := a.state.QueuePayloads[input.ID]
			found = found && ok && prepared.Harness == s.Harness && prepared.Directory == folder
		}
		if !available || !found || !installed || payload.Harness != s.Harness || payload.Directory != folder {
			_ = a.commitLocked(func(d *diskState) {
				pauseMessageQueue(d.session(s.ID), "Project or harness changed. Remove this message and send it again.")
			})
			continue
		}
		combined, err := queuedSubmission(&a.state, messages)
		if err == nil {
			err = validateSubmissionSize(combined, s.Harness)
		}
		if err != nil {
			_ = a.commitLocked(func(d *diskState) { pauseMessageQueue(d.session(s.ID), err.Error()) })
			continue
		}
		cwd, err := existingDirectory(folder)
		if err != nil {
			_ = a.commitLocked(func(d *diskState) { pauseMessageQueue(d.session(s.ID), err.Error()) })
			continue
		}
		if err := a.commitLocked(func(d *diskState) {
			for _, input := range messages {
				appendQueuedUser(d, s.ID, input)
			}
			next := d.session(s.ID)
			next.Status, next.Permissions, next.Questions = "running", nil, nil
		}); err != nil {
			return
		}
		ctx, cancel := context.WithCancel(a.ctx)
		t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
		a.runs[s.ID] = t
		a.wg.Add(1)
		go a.execute(t, *a.state.session(s.ID), a.state.Native[s.ID], b, cwd, combined)
	}
}

func (a *app) changeQueuedMessage(w http.ResponseWriter, r *http.Request) {
	id, messageID := r.PathValue("id"), r.PathValue("messageID")
	a.mu.Lock()
	_, err := a.changeQueuedMessageLocked(id, messageID, r.Method == http.MethodDelete, true, nil)
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	if err != nil {
		failSessionControl(w, err)
		return
	}
	respond(w, 200, response)
}

func (a *app) changeQueuedMessageLocked(id, messageID string, remove, retryUncertain bool, origin *SpawnOrigin) (string, error) {
	s := a.state.session(id)
	if s == nil {
		return "", controlError(404, "Session not found.")
	}
	if !remove && a.state.conversationArchived(s) {
		return "", controlError(409, "Restore the conversation before sending queued messages.")
	}
	var queued *QueuedMessage
	for _, q := range s.Queue {
		if q.ID == messageID {
			copy := q
			queued = &copy
			break
		}
	}
	if queued == nil {
		return "", controlError(404, "Queued message not found.")
	}
	if queued.Status == "sending" || queued.Status == "interrupting" || remove && queued.Status == "accepted" {
		return "", controlError(409, "Message delivery is in progress.")
	}
	if !remove && queued.Status == "uncertain" && !retryUncertain {
		return "", controlError(409, "Native delivery is uncertain. Obtain the user's explicit retry decision and pass retryUncertain:true; this may duplicate an instruction already received.")
	}
	if t := a.runs[id]; t != nil && t.ctx.Err() != nil {
		return "", controlError(409, "Execution is still stopping.")
	}
	if a.closing {
		return "", controlError(503, "Engine is shutting down.")
	}
	if err := a.commitLocked(func(d *diskState) {
		if remove {
			removeQueuedMessage(d, id, messageID)
			appendSessionControl(d.session(id), "Queued message removed", origin, map[string]any{"messageId": messageID})
			return
		}
		next := d.session(id)
		for i := range next.Queue {
			if next.Queue[i].ID == messageID {
				if next.Queue[i].Status == "uncertain" || next.Queue[i].Status == "accepted" {
					replacementID := newID()
					d.QueuePayloads[replacementID] = d.QueuePayloads[messageID]
					delete(d.QueuePayloads, messageID)
					next.Queue[i].ID = replacementID
					messageID = replacementID
				}
			}
		}
		prepareMessageInterrupt(d, id, messageID, a.runs[id] != nil)
		appendSessionControl(next, "Interrupt and send accepted", origin, map[string]any{"messageId": messageID})
	}); err != nil {
		return "", err
	}
	if !remove {
		a.interruptForMessageLocked(id, messageID)
	}
	a.wakeSteeringLocked(id)
	a.scheduleMessagesLocked()
	return messageID, nil
}
