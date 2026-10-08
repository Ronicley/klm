package main

import (
	"context"
	"errors"
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
	Files    []ChatFile        `json:"files,omitempty"`
}
type queuedPayload struct {
	Submission submission
	Harness    string
	Directory  string
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
	if len(q.Files) > 0 {
		e.Data["files"] = q.Files
	}
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
		case "sending":
			q.Status, q.Error = "uncertain", "Delivery was not confirmed. Check the conversation before sending again."
		case "queued", "steering":
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

// Native adapters claim one input at a time and correlate its acknowledgement.
func (p *adapter) takeSteering() (*QueuedMessage, submission, error) {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || p.turn.ctx.Err() != nil {
		return nil, submission{}, nil
	}
	s := a.state.session(p.id)
	for _, q := range s.Queue {
		if q.Status == "sending" {
			return nil, submission{}, nil
		}
	}
	for _, q := range s.Queue {
		if q.Status != "steering" {
			continue
		}
		payload, ok := a.state.QueuePayloads[q.ID]
		if !ok {
			return nil, submission{}, errors.New("Queued message content is unavailable.")
		}
		err := a.commitLocked(func(d *diskState) {
			for i := range d.session(p.id).Queue {
				if d.session(p.id).Queue[i].ID == q.ID {
					d.session(p.id).Queue[i].Status = "sending"
				}
			}
			d.session(p.id).UpdatedAt = now()
		})
		if err != nil {
			return nil, submission{}, err
		}
		return &q, payload.Submission, nil
	}
	return nil, submission{}, nil
}

func (p *adapter) finishSteering(id, failure string) error {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	var found *QueuedMessage
	for _, q := range a.state.session(p.id).Queue {
		if q.ID == id && (q.Status == "sending" || q.Status == "uncertain") {
			copy := q
			found = &copy
			break
		}
	}
	if found == nil {
		return nil
	}
	err := a.commitLocked(func(d *diskState) {
		if failure == "" {
			appendQueuedUser(d, p.id, *found)
			return
		}
		s := d.session(p.id)
		for i := range s.Queue {
			if s.Queue[i].ID == id {
				s.Queue[i].Status, s.Queue[i].Error = "paused", failure
			}
		}
		s.UpdatedAt = now()
	})
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
		q := s.Queue[0]
		if q.Status != "queued" && q.Status != "steering" {
			continue
		}
		folder, available := a.state.conversationDirectory(&s)
		payload, found := a.state.QueuePayloads[q.ID]
		b, installed := a.binaries[s.Harness]
		if !available || !found || !installed || payload.Harness != s.Harness || payload.Directory != folder {
			_ = a.commitLocked(func(d *diskState) {
				pauseMessageQueue(d.session(s.ID), "Project or harness changed. Remove this message and send it again.")
			})
			continue
		}
		cwd, err := existingDirectory(folder)
		if err != nil {
			_ = a.commitLocked(func(d *diskState) { pauseMessageQueue(d.session(s.ID), err.Error()) })
			continue
		}
		if err := a.commitLocked(func(d *diskState) {
			appendQueuedUser(d, s.ID, q)
			next := d.session(s.ID)
			next.Status, next.Permissions, next.Questions = "running", nil, nil
		}); err != nil {
			return
		}
		ctx, cancel := context.WithCancel(a.ctx)
		t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
		a.runs[s.ID] = t
		a.wg.Add(1)
		go a.execute(t, *a.state.session(s.ID), a.state.Native[s.ID], b, cwd, payload.Submission)
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
	if queued.Status == "sending" {
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
				if next.Queue[i].Status == "uncertain" {
					replacementID := newID()
					d.QueuePayloads[replacementID] = d.QueuePayloads[messageID]
					delete(d.QueuePayloads, messageID)
					next.Queue[i].ID = replacementID
					messageID = replacementID
				}
				next.Queue[i].Status, next.Queue[i].Mode, next.Queue[i].Error = "steering", "steer", ""
			}
		}
		next.UpdatedAt = now()
		appendSessionControl(next, "Queued message send accepted", origin, map[string]any{"messageId": messageID})
	}); err != nil {
		return "", err
	}
	// Send now may intentionally promote an item ahead of the rest of the queue.
	if a.runs[id] == nil && !remove {
		if err := a.commitLocked(func(d *diskState) {
			next := d.session(id)
			for i, q := range next.Queue {
				if q.ID == messageID {
					next.Queue = append([]QueuedMessage{q}, append(next.Queue[:i], next.Queue[i+1:]...)...)
					break
				}
			}
		}); err != nil {
			return "", err
		}
	}
	a.wakeSteeringLocked(id)
	a.scheduleMessagesLocked()
	return messageID, nil
}
