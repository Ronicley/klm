package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type QuestionItem struct {
	ID       string           `json:"id"`
	Header   string           `json:"header"`
	Text     string           `json:"text"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple"`
	Custom   bool             `json:"custom"`
	Secret   bool             `json:"secret"`
}

type QuestionRequest struct {
	ID        string         `json:"id"`
	Harness   string         `json:"harness"`
	Items     []QuestionItem `json:"items"`
	CreatedAt string         `json:"createdAt"`
	Resolving bool           `json:"resolving,omitempty"`
	SourceID  string         `json:"-"`
}

type pendingQuestion struct {
	request QuestionRequest
	reply   func([][]string, bool) error
	busy    bool
}

func (p *adapter) requestQuestion(req QuestionRequest, reply func([][]string, bool) error) error {
	if req.SourceID == "" || len(req.Items) == 0 || len(req.Items) > 12 || reply == nil {
		return errors.New("Harness question request is incomplete or too large.")
	}
	ids := map[string]bool{}
	for i := range req.Items {
		q := &req.Items[i]
		if q.ID == "" || ids[q.ID] || strings.TrimSpace(q.Text) == "" || len(q.Text) > 16384 || len(q.Options) > 64 {
			return errors.New("Harness question has invalid text, identifiers, or options.")
		}
		ids[q.ID] = true
		if q.Options == nil {
			q.Options = []QuestionOption{}
		}
		if len(q.Options) == 0 {
			q.Custom = true
		}
		labels := map[string]bool{}
		for _, option := range q.Options {
			if strings.TrimSpace(option.Label) == "" || len(option.Label) > 1024 || labels[option.Label] {
				return errors.New("Harness question contains invalid or duplicate options.")
			}
			labels[option.Label] = true
		}
	}
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runs[p.id] != p.turn || p.turn.ctx.Err() != nil || a.storageErr != nil {
		return errors.New("Question belongs to an inactive turn.")
	}
	for _, pending := range p.turn.questions {
		if pending.request.SourceID == req.SourceID {
			return nil
		}
	}
	req.ID, req.Harness, req.CreatedAt = newID(), p.harness, now()
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.Questions = append(s.Questions, req)
		s.UpdatedAt = now()
	}); err != nil {
		return err
	}
	if p.turn.questions == nil {
		p.turn.questions = map[string]*pendingQuestion{}
	}
	p.turn.questions[req.ID] = &pendingQuestion{request: req, reply: reply}
	return nil
}

func (p *adapter) dismissQuestion(sourceID string) error {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, pending := range p.turn.questions {
		if pending.request.SourceID != sourceID {
			continue
		}
		delete(p.turn.questions, id)
		return a.commitLocked(func(d *diskState) {
			s := d.session(p.id)
			s.Questions = slices.DeleteFunc(s.Questions, func(q QuestionRequest) bool { return q.ID == id })
			s.UpdatedAt = now()
		})
	}
	return nil
}

func (a *app) answerQuestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answers   [][]string `json:"answers"`
		Cancelled bool       `json:"cancelled"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, questionID := r.PathValue("id"), r.PathValue("questionID")
	a.mu.Lock()
	if s := a.state.session(id); s != nil && s.Role == sessionRoleSubagent {
		a.mu.Unlock()
		fail(w, 404, "Conversation not found.")
		return
	}
	t := a.runs[id]
	if t == nil || t.ctx.Err() != nil || t.questions[questionID] == nil {
		a.mu.Unlock()
		fail(w, 409, "This question is no longer waiting for an answer.")
		return
	}
	pending := t.questions[questionID]
	if pending.busy {
		a.mu.Unlock()
		fail(w, 409, "This answer is already being sent.")
		return
	}
	valid := body.Cancelled && len(body.Answers) == 0
	if !body.Cancelled && len(body.Answers) == len(pending.request.Items) {
		valid = true
		for i, q := range pending.request.Items {
			answers := body.Answers[i]
			if len(answers) == 0 || len(answers) > 65 || (!q.Multiple && len(answers) != 1) {
				valid = false
				break
			}
			seen := map[string]bool{}
			for _, answer := range answers {
				if strings.TrimSpace(answer) == "" || len(answer) > 16384 || !utf8.ValidString(answer) || strings.ContainsRune(answer, 0) || seen[answer] {
					valid = false
					break
				}
				seen[answer] = true
				if !q.Custom && !slices.ContainsFunc(q.Options, func(option QuestionOption) bool { return option.Label == answer }) {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
		}
	}
	if !valid {
		a.mu.Unlock()
		fail(w, 400, "Answer every question using the offered choices or an allowed custom answer.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		for i := range s.Questions {
			if s.Questions[i].ID == questionID {
				s.Questions[i].Resolving = true
			}
		}
		s.UpdatedAt = now()
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	pending.busy = true
	a.mu.Unlock()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
	err := t.ctx.Err()
	if err == nil {
		err = pending.reply(body.Answers, body.Cancelled)
	}
	a.mu.Lock()
	if err != nil {
		pending.busy = false
		if err := a.commitLocked(func(d *diskState) {
			s := d.session(id)
			for i := range s.Questions {
				if s.Questions[i].ID == questionID {
					s.Questions[i].Resolving = false
				}
			}
			s.UpdatedAt = now()
		}); err != nil {
			a.mu.Unlock()
			fail(w, 503, err.Error())
			return
		}
		a.mu.Unlock()
		fail(w, 409, "Could not deliver the answer. The question may have expired; refresh or stop the turn.")
		return
	}
	delete(t.questions, questionID)
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		s.Questions = slices.DeleteFunc(s.Questions, func(q QuestionRequest) bool { return q.ID == questionID })
		s.UpdatedAt = now()
		entry := event("status", "Question dismissed.")
		entry.Status = "cancelled"
		if !body.Cancelled {
			parts := []string{}
			for i, q := range pending.request.Items {
				answer := strings.Join(body.Answers[i], ", ")
				if q.Secret {
					answer = "[Private answer sent]"
				}
				parts = append(parts, q.Text+"\n\n"+answer)
			}
			entry = event("user", strings.Join(parts, "\n\n"))
		}
		entry.Title = "Question response"
		entry.ConsultationID = t.consultationID
		s.Events = append(s.Events, entry)
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	respond(w, 200, response)
}
