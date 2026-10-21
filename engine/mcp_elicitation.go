package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPElicitation struct {
	ConnectionID   string         `json:"connectionId"`
	ConnectionName string         `json:"connectionName"`
	Mode           string         `json:"mode"`
	Message        string         `json:"message"`
	URL            string         `json:"url,omitempty"`
	Schema         map[string]any `json:"schema,omitempty"`
	CallID         string         `json:"callId"`
}

// The Pi gateway serializes dispatch, so a server request has one exact owning
// call. Unsolicited and initialization-time elicitation is explicitly cancelled.
func (p *adapter) mcpElicit(ctx context.Context, c MCPConnectionSnapshot, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	cancelled := &mcp.ElicitResult{Action: "cancel"}
	if p.mcp == nil || req.Params == nil {
		return cancelled, nil
	}
	p.mcp.mu.Lock()
	callID := p.mcp.activeCalls[c.ID]
	p.mcp.mu.Unlock()
	if callID == "" || p.turn.ctx.Err() != nil || p.graphSealed() {
		return cancelled, nil
	}
	params := req.Params
	question := &MCPElicitation{ConnectionID: c.ID, ConnectionName: c.Name, Message: params.Message, Mode: params.Mode, CallID: callID}
	if question.Mode == "" {
		question.Mode = "form"
		if params.URL != "" {
			question.Mode = "url"
		}
	}
	if len(question.Message) == 0 || len(question.Message) > 16384 {
		return nil, errors.New("Unsupported MCP elicitation message.")
	}
	switch question.Mode {
	case "url":
		u, err := url.Parse(params.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "https" || len(params.URL) > 8192 || params.ElicitationID == "" {
			return nil, errors.New("URL elicitation requires an explicit HTTPS URL and elicitation ID.")
		}
		question.URL = params.URL
	case "form":
		data, err := json.Marshal(params.RequestedSchema)
		if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &question.Schema) != nil {
			return nil, errors.New("Invalid MCP elicitation form.")
		}
		if err := supportedElicitationSchema(question.Schema); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("Unsupported MCP elicitation mode.")
	}
	source := "elicitation/" + newID()
	answer := make(chan *mcp.ElicitResult, 1)
	// Reuse pending-question storage and graph-owner projection. Only the explicit
	// elicitation endpoint can reply; native/general-agent question tools cannot.
	reqQuestion := QuestionRequest{SourceID: source, Elicitation: question, Items: []QuestionItem{{ID: "elicitation", Text: params.Message, Options: []QuestionOption{{Label: "Accept"}, {Label: "Decline"}}}}}
	err := p.requestQuestion(reqQuestion, func([][]string, bool) error { return errors.New("Use the MCP elicitation form.") })
	if err != nil {
		return cancelled, err
	}
	defer p.dismissQuestion(source)
	p.app.mu.Lock()
	for _, pending := range p.turn.questions {
		if pending.request.SourceID == source {
			pending.elicitReply = func(result *mcp.ElicitResult) error {
				select {
				case answer <- result:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				case <-p.turn.ctx.Done():
					return p.turn.ctx.Err()
				}
			}
		}
	}
	p.app.mu.Unlock()
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case result := <-answer:
		return result, nil
	case <-ctx.Done():
		return cancelled, nil
	case <-p.turn.ctx.Done():
		return cancelled, nil
	case <-timer.C:
		return cancelled, nil
	}
}

func supportedElicitationSchema(schema map[string]any) error {
	if str(schema, "type") != "object" {
		return errors.New("Elicitation requires a flat object form.")
	}
	properties := object(schema["properties"])
	if len(properties) > 32 {
		return errors.New("Elicitation supports at most 32 fields.")
	}
	for name, value := range properties {
		field := object(value)
		kind := str(field, "type")
		if name == "" || len(name) > 200 || field == nil {
			return errors.New("Invalid elicitation field.")
		}
		if !slices.Contains([]string{"string", "number", "integer", "boolean", "array"}, kind) {
			return errors.New("Nested elicitation fields are not supported.")
		}
		if kind == "array" {
			items := object(field["items"])
			if str(items, "type") != "string" || items["enum"] == nil {
				return errors.New("Elicitation arrays require string enum options.")
			}
		}
		if strings.EqualFold(str(field, "format"), "password") {
			return errors.New("Sensitive credentials must use URL elicitation, not a form.")
		}
	}
	_, err := resolveMCPSchema(schema)
	return err
}

func (a *app) answerMCPElicitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !slices.Contains([]string{"accept", "decline", "cancel"}, body.Action) {
		fail(w, 400, "Choose accept, decline or cancel.")
		return
	}
	id, qid := r.PathValue("id"), r.PathValue("questionID")
	a.mu.Lock()
	t := a.runs[id]
	if t == nil || t.ctx.Err() != nil || t.questions[qid] == nil || t.questions[qid].request.Elicitation == nil || t.questions[qid].elicitReply == nil {
		a.mu.Unlock()
		fail(w, 409, "This elicitation is no longer active.")
		return
	}
	pending := t.questions[qid]
	question := pending.request.Elicitation
	if pending.busy {
		a.mu.Unlock()
		fail(w, 409, "This reply is already being delivered.")
		return
	}
	if body.Action == "accept" && question.Mode == "form" {
		resolved, err := resolveMCPSchema(question.Schema)
		if err != nil || resolved.Validate(body.Content) != nil {
			a.mu.Unlock()
			fail(w, 400, "Complete the form using the requested field types and options.")
			return
		}
	} else if len(body.Content) > 0 {
		a.mu.Unlock()
		fail(w, 400, "Only accepted forms may include content.")
		return
	}
	pending.busy = true
	a.mu.Unlock()
	err := pending.elicitReply(&mcp.ElicitResult{Action: body.Action, Content: body.Content})
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		pending.busy = false
		fail(w, 409, "Elicitation expired before the reply was delivered.")
		return
	}
	delete(t.questions, qid)
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		s.Questions = slices.DeleteFunc(s.Questions, func(q QuestionRequest) bool { return q.ID == qid })
		s.UpdatedAt = now()
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, a.currentSessionUpdateLocked(id))
}
