package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
)

type Permission struct {
	mcpTool     bool           // Adapter-confirmed MCP identity; never inferred from tool arguments.
	ID          string         `json:"id"`
	Harness     string         `json:"harness"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Patterns    []string       `json:"patterns"`
	Details     map[string]any `json:"details,omitempty"`
	Command     string         `json:"command,omitempty"`
	Path        string         `json:"path,omitempty"`
	ScopeLabel  string         `json:"scopeLabel,omitempty"`
	Dangerous   bool           `json:"dangerous,omitempty"`
	Decisions   []string       `json:"decisions"`
	AllowLabel  string         `json:"allowLabel,omitempty"`
	CreatedAt   string         `json:"createdAt"`
	Resolving   bool           `json:"resolving,omitempty"`
	SourceID    string         `json:"-"`
}

type permissionGrant struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	Harness   string `json:"harness"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	CreatedAt string `json:"createdAt"`
	Decision  string `json:"decision,omitempty"` // Empty is a legacy allow.
	Label     string `json:"label,omitempty"`
}

type pendingApproval struct {
	request     Permission
	key         string
	reply       func(bool) error
	busy        bool
	ruleHarness string
	ruleKind    string
	ruleKey     string
}

func (p *adapter) requestPermission(req Permission, scope map[string]any, reply func(bool) error) error {
	if err := p.turn.ctx.Err(); err != nil {
		return err
	}
	if req.SourceID == "" || req.Kind == "" || reply == nil {
		return errors.New("Harness permission request is incomplete.")
	}
	key := ""
	if scope != nil {
		encoded, err := json.Marshal(scope)
		if err != nil {
			return errors.New("Could not encode the permission scope.")
		}
		sum := sha256.Sum256(encoded)
		key = hex.EncodeToString(sum[:])
	}
	if len(req.Decisions) == 0 {
		req.Decisions = []string{"once", "session", "always", "reject"}
	}
	if key == "" {
		req.Decisions = slices.DeleteFunc(slices.Clone(req.Decisions), func(choice string) bool { return choice == "session" || choice == "always" })
	}
	facts := permissionFactsFor(req, p.harness, p.cwd)
	req.Dangerous = facts.dangerous
	ruleKey, ruleKind, ruleHarness := key, req.Kind, p.harness
	if facts.key != "" && key != "" {
		ruleKey, ruleKind, ruleHarness = facts.key, facts.operation, ""
	}
	if ruleKey != "" && slices.Contains(req.Decisions, "always") {
		req.Decisions = append(req.Decisions, "deny_project", "allow_global", "deny_global")
		req.ScopeLabel = facts.label
		if req.ScopeLabel == "" {
			req.ScopeLabel = "This exact request (" + p.harness + ")"
		}
	}
	req.ID, req.Harness, req.CreatedAt = newID(), p.harness, now()
	if req.Patterns == nil {
		req.Patterns = []string{}
	}
	a := p.app
	a.mu.Lock()
	if a.runs[p.id] != p.turn || p.turn.ctx.Err() != nil || a.storageErr != nil {
		a.mu.Unlock()
		return errors.New("Permission request belongs to an inactive turn.")
	}
	for _, existing := range p.turn.approvals {
		if existing.request.SourceID == req.SourceID {
			a.mu.Unlock()
			return nil
		}
	}
	s := a.state.session(p.id)
	if s == nil {
		a.mu.Unlock()
		return errors.New("Permission session no longer exists.")
	}
	if s.ProjectID == "" {
		// An engine-owned conversation has no project-level permission lifetime.
		req.Decisions = slices.DeleteFunc(slices.Clone(req.Decisions), func(choice string) bool { return choice == "always" || choice == "deny_project" })
	}
	decision := ""
	reason := "saved rule"
	if s.YOLO {
		reason = "YOLO mode"
		decision = "deny"
		if slices.Contains(req.Decisions, "once") {
			decision = "allow"
		}
	} else {
		decision = rememberedPermission(a.state.Grants, s, ruleHarness, ruleKind, ruleKey)
	}
	if decision == "deny" || decision == "allow" && slices.Contains(req.Decisions, "once") {
		a.mu.Unlock()
		return p.replyAutomaticPermission(req, decision, reason, reply)
	}
	for _, grant := range a.state.Grants {
		if key != "" && slices.Contains(req.Decisions, "once") && grant.ProjectID == s.ProjectID &&
			grant.Harness == p.harness && grant.Kind == req.Kind && grant.Key == key && grant.Decision != "deny" && (grant.SessionID == "" || grant.SessionID == p.id) {
			a.mu.Unlock()
			return p.replyAutomaticPermission(req, "allow", "remembered exact request", reply)
		}
	}
	if facts.decision == "deny" || facts.decision == "allow" && slices.Contains(req.Decisions, "once") {
		a.mu.Unlock()
		return p.replyAutomaticPermission(req, facts.decision, "workspace policy", reply)
	}
	err := a.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.Permissions = append(s.Permissions, req)
		s.UpdatedAt = now()
	})
	if err == nil {
		if p.turn.approvals == nil {
			p.turn.approvals = map[string]*pendingApproval{}
		}
		p.turn.approvals[req.ID] = &pendingApproval{request: req, key: key, reply: reply, ruleKey: ruleKey, ruleKind: ruleKind, ruleHarness: ruleHarness}
	}
	a.mu.Unlock()
	return err
}

func (p *adapter) dismissPermission(sourceID string) error {
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, pending := range p.turn.approvals {
		if pending.request.SourceID != sourceID {
			continue
		}
		delete(p.turn.approvals, id)
		return a.commitLocked(func(d *diskState) {
			s := d.session(p.id)
			s.Permissions = slices.DeleteFunc(s.Permissions, func(req Permission) bool { return req.ID == id })
			s.UpdatedAt = now()
		})
	}
	return nil
}

func (a *app) permissionDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if err := a.replyConversationPermission(id, r.PathValue("permissionID"), body.Decision, nil, nil); err != nil {
		failSessionControl(w, err)
		return
	}
	a.mu.Lock()
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	respond(w, 200, response)
}

func permissionDecisionScope(decision string) string {
	switch decision {
	case "session":
		return "session"
	case "always", "deny_project":
		return "project"
	case "allow_global", "deny_global":
		return "global"
	default:
		return "request"
	}
}

func (a *app) replyConversationPermission(id, requestID, decision string, origin *SpawnOrigin, validateLocked func() error) error {
	resolveRelated := false
	defer func() {
		if resolveRelated {
			a.resolveRememberedPermissions()
		}
	}()
	a.mu.Lock()
	if validateLocked != nil {
		if err := validateLocked(); err != nil {
			a.mu.Unlock()
			return err
		}
	}
	if origin != nil {
		if t := a.runs[id]; t == nil || t.approvals[requestID] == nil {
			for _, request := range a.state.graphProjection(id).Requests {
				if request.Kind == "permission" && request.RequestID == requestID {
					id = request.SessionID
					break
				}
			}
		}
	}
	if s := a.state.session(id); s != nil && s.Role == sessionRoleSubagent {
		a.mu.Unlock()
		return controlError(404, "Conversation not found.")
	}
	t := a.runs[id]
	if t == nil || t.ctx.Err() != nil || t.approvals[requestID] == nil {
		a.mu.Unlock()
		return controlError(409, "This permission request is no longer pending.")
	}
	pending := t.approvals[requestID]
	if pending.busy || !slices.Contains(pending.request.Decisions, decision) ||
		!slices.Contains([]string{"once", "session", "always", "reject", "deny_project", "allow_global", "deny_global"}, decision) {
		a.mu.Unlock()
		return controlError(409, "This permission decision is unavailable or already being processed.")
	}
	grantID := ""
	err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		if slices.Contains([]string{"session", "always", "deny_project", "allow_global", "deny_global"}, decision) {
			grantID = newID()
			grant := permissionGrant{ID: grantID, ProjectID: s.ProjectID, Harness: pending.ruleHarness, Kind: pending.ruleKind, Key: pending.ruleKey, CreatedAt: now(), Decision: "allow", Label: pending.request.ScopeLabel}
			if decision == "session" {
				grant.SessionID = id
			}
			if decision == "allow_global" || decision == "deny_global" {
				grant.ProjectID = ""
			}
			if decision == "deny_project" || decision == "deny_global" {
				grant.Decision = "deny"
			}
			d.Grants = append(d.Grants, grant)
		}
		for i := range s.Permissions {
			if s.Permissions[i].ID == requestID {
				s.Permissions[i].Resolving = true
			}
		}
		s.UpdatedAt = now()
	})
	if err != nil {
		a.mu.Unlock()
		return err
	}
	pending.busy = true
	a.mu.Unlock()
	if t.ctx.Err() != nil {
		err = t.ctx.Err()
	} else {
		err = pending.reply(decision != "reject" && decision != "deny_project" && decision != "deny_global")
	}
	a.mu.Lock()
	if err != nil {
		rollback := a.commitLocked(func(d *diskState) {
			d.Grants = slices.DeleteFunc(d.Grants, func(grant permissionGrant) bool { return grant.ID == grantID && grantID != "" })
			s := d.session(id)
			for i := range s.Permissions {
				if s.Permissions[i].ID == requestID {
					s.Permissions[i].Resolving = false
				}
			}
			s.UpdatedAt = now()
		})
		pending.busy = false
		a.mu.Unlock()
		if rollback != nil {
			return rollback
		}
		return controlError(409, "Could not confirm delivery of the permission decision. Native delivery may have occurred; refresh the owning conversation or stop the turn before retrying.")
	}
	delete(t.approvals, requestID)
	resolveRelated = grantID != ""
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		s.Permissions = slices.DeleteFunc(s.Permissions, func(req Permission) bool { return req.ID == requestID })
		s.UpdatedAt = now()
		entry := event("status", "Permission decision: "+decision)
		entry.ConsultationID = t.consultationID
		entry.Title, entry.Status = pending.request.Title, "completed"
		entry.Data = map[string]any{"harness": s.Harness, "permission": pending.request.Kind, "patterns": pending.request.Patterns, "decision": decision, "scope": pending.request.ScopeLabel, "decisionScope": permissionDecisionScope(decision)}
		if origin != nil {
			entry.Data["origin"] = origin
		}
		s.Events = append(s.Events, entry)
	}); err != nil {
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	return nil
}
