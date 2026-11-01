package main

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func (st *sqliteSessionTx) requestKey(table, owner, id string) (string, error) {
	var key string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_`+table+` WHERE owner_id=? AND record_id=?`, owner, id).Scan(&key)
	return key, err
}
func (st *sqliteSessionTx) sessionYOLO(s Session) (bool, error) {
	if s.Role == sessionRoleSubagent {
		child, err := st.delegationChild(s.ID)
		if err == nil {
			parent, err := st.sessionMeta(child.ParentSessionID)
			if err != nil {
				return false, err
			}
			return st.sessionYOLO(parent)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	r, err := st.activeAutomation(s.ID)
	if err != nil {
		return false, err
	}
	if r != nil {
		return r.Snapshot.YOLO, nil
	}
	return s.YOLO, nil
}
func (st *sqliteSessionTx) remembered(s Session, harness, kind, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	var decision string
	err := st.tx.QueryRowContext(st.ctx, `SELECT x_decision FROM data_permission_grants g WHERE x_harness=? AND x_kind=? AND x_key=? AND project_id IN ('',?) AND session_id IN ('',?) AND NOT EXISTS(SELECT 1 FROM permission_reply_intents i WHERE i.grant_id=g.id) ORDER BY (x_decision='deny') DESC,position LIMIT 1`, harness, kind, key, s.ProjectID, s.ID).Scan(&decision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if decision == "deny" {
		return decision, nil
	}
	return "allow", nil
}

func (p *adapter) requestSQLPermissionLocked(req *Permission, key, ruleHarness, ruleKind, ruleKey, workspaceDecision string, reply func(bool) error) (string, string, error) {
	var decision, reason string
	reason = "saved rule"
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		if s.ProjectID == "" {
			req.Decisions = slices.DeleteFunc(slices.Clone(req.Decisions), func(value string) bool { return value == "always" || value == "deny_project" })
		}
		yolo, err := st.sessionYOLO(s)
		if err != nil {
			return err
		}
		if yolo {
			reason, decision = "YOLO mode", "deny"
			if slices.Contains(req.Decisions, "once") {
				decision = "allow"
			}
		} else {
			decision, err = st.remembered(s, ruleHarness, ruleKind, ruleKey)
			if err != nil {
				return err
			}
		}
		if decision == "deny" || decision == "allow" && slices.Contains(req.Decisions, "once") {
			return nil
		}
		if key != "" && slices.Contains(req.Decisions, "once") {
			var found bool
			err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_permission_grants g WHERE project_id=? AND x_harness=? AND x_kind=? AND x_key=? AND x_decision<>'deny' AND session_id IN ('',?) AND NOT EXISTS(SELECT 1 FROM permission_reply_intents i WHERE i.grant_id=g.id))`, s.ProjectID, p.harness, req.Kind, key, p.id).Scan(&found)
			if err != nil {
				return err
			}
			if found {
				decision, reason = "allow", "remembered exact request"
				return nil
			}
		}
		if workspaceDecision == "deny" || workspaceDecision == "allow" && slices.Contains(req.Decisions, "once") {
			decision, reason = workspaceDecision, "workspace policy"
			return nil
		}
		decision = ""
		if err := st.analyticsWait(p.turn, req.ID, true, p.analyticsOwnRequest(req.analyticsOrigin), p.harness == "pi"); err != nil {
			return err
		}
		if err := st.putRecord("pending_permissions", sqliteOwnedKey(p.id, req.ID), p.id, req.ID, -1, *req); err != nil {
			return err
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	if err != nil {
		return "", "", err
	}
	if decision == "" {
		if p.turn.approvals == nil {
			p.turn.approvals = map[string]*pendingApproval{}
		}
		copy := *req
		p.turn.approvals[req.ID] = &pendingApproval{request: copy, key: key, ruleHarness: ruleHarness, ruleKind: ruleKind, ruleKey: ruleKey, reply: func(allow bool) error {
			if err := reply(allow); err != nil {
				return err
			}
			return p.analyticsWaitEnd(copy.ID)
		}}
	}
	return decision, reason, nil
}

func (a *app) sqlRequestOwnerLocked(id, requestID, kind string, origin *SpawnOrigin) (string, error) {
	meta, err := a.store.readSessionMeta(context.Background(), id)
	if err != nil {
		return "", err
	}
	if meta.Role == sessionRoleSubagent {
		return "", controlError(404, "Conversation not found.")
	}
	t := a.runs[id]
	present := t != nil && (kind == "permission" && t.approvals[requestID] != nil || kind == "question" && t.questions[requestID] != nil)
	if present {
		return id, nil
	}
	// A managed child is read-only publicly. Replies target the owner's exact
	// pending request and active lease, while the grant remains child-scoped.
	for childID, childTurn := range a.runs {
		binding := childTurn.delegationChild
		if binding == nil || childTurn.ctx.Err() != nil {
			continue
		}
		ownerID := binding.owner.parent.id
		if graph := binding.owner.parent.turn.graphDelegation; graph != nil {
			ownerID = graph.OwnerID
		}
		if ownerID != id {
			continue
		}
		if kind == "permission" && childTurn.approvals[requestID] != nil || kind == "question" && childTurn.questions[requestID] != nil {
			return childID, nil
		}
	}
	if origin == nil {
		return id, nil
	}
	table := "pending_permissions"
	if kind == "question" {
		table = "pending_questions"
	}
	var owner string
	err = a.store.readers.QueryRow(`SELECT s.id FROM data_`+table+` p JOIN data_sessions s ON s.id=p.owner_id JOIN data_graph_runs r ON r.id=s.x_graphRunId WHERE p.record_id=? AND r.x_conversationId=? AND r.status IN ('starting','running','ending') LIMIT 1`, requestID, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return id, nil
	}
	return owner, err
}

func (a *app) replySQLPermission(id, requestID, decision string, origin *SpawnOrigin, validate func() error) error {
	a.mu.Lock()
	if validate != nil {
		if err := validate(); err != nil {
			a.mu.Unlock()
			return err
		}
	}
	var err error
	id, err = a.sqlRequestOwnerLocked(id, requestID, "permission", origin)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	t := a.runs[id]
	if t == nil || t.ctx.Err() != nil || t.approvals[requestID] == nil {
		a.mu.Unlock()
		return controlError(409, "This permission request is no longer pending.")
	}
	pending := t.approvals[requestID]
	if pending.busy || !slices.Contains(pending.request.Decisions, decision) || !slices.Contains([]string{"once", "session", "always", "reject", "deny_project", "allow_global", "deny_global"}, decision) {
		a.mu.Unlock()
		return controlError(409, "This permission decision is unavailable or already being processed.")
	}
	grantID := ""
	err = a.writeSQLLocked(t.ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if slices.Contains([]string{"session", "always", "deny_project", "allow_global", "deny_global"}, decision) {
			grantID = newID()
			g := permissionGrant{ID: grantID, ProjectID: s.ProjectID, Harness: pending.ruleHarness, Kind: pending.ruleKind, Key: pending.ruleKey, CreatedAt: now(), Decision: "allow", Label: pending.request.ScopeLabel}
			if decision == "session" {
				g.SessionID = id
			}
			if decision == "allow_global" || decision == "deny_global" {
				g.ProjectID = ""
			}
			if decision == "deny_global" || decision == "deny_project" {
				g.Decision = "deny"
			}
			if err := st.putRoot("permission_grants", g.ID, g); err != nil {
				return err
			}
		}
		if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO permission_reply_intents(request_id,session_id,grant_id) VALUES(?,?,?)`, requestID, id, grantID); err != nil {
			return err
		}
		key, err := st.requestKey("pending_permissions", id, requestID)
		if err != nil {
			return err
		}
		req := pending.request
		req.Resolving = true
		if err := st.putExistingRecord("pending_permissions", key, req); err != nil {
			return err
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	if err != nil {
		a.mu.Unlock()
		return err
	}
	pending.busy = true
	a.mu.Unlock()
	err = t.ctx.Err()
	if err == nil {
		err = pending.reply(decision != "reject" && decision != "deny_project" && decision != "deny_global")
	}
	a.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deliveryErr := err
	err = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		key, readErr := st.requestKey("pending_permissions", id, requestID)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return readErr
		}
		if deliveryErr != nil {
			if grantID != "" {
				if err := st.deleteRecord("permission_grants", grantID); err != nil {
					return err
				}
			}
			if key != "" {
				req := pending.request
				req.Resolving = false
				if err := st.putExistingRecord("pending_permissions", key, req); err != nil {
					return err
				}
			}
		} else {
			if key != "" {
				if err := st.deleteRecord("pending_permissions", key); err != nil {
					return err
				}
			}
			e := event("status", "Permission decision: "+decision)
			e.Title, e.Status, e.ConsultationID = pending.request.Title, "completed", t.consultationID
			e.Data = map[string]any{"harness": pending.request.Harness, "permission": pending.request.Kind, "patterns": pending.request.Patterns, "decision": decision, "scope": pending.request.ScopeLabel, "decisionScope": permissionDecisionScope(decision)}
			if origin != nil {
				e.Data["origin"] = origin
			}
			if _, err := st.appendEvent(id, e); err != nil {
				return err
			}
		}
		if _, err := st.tx.ExecContext(ctx, `DELETE FROM permission_reply_intents WHERE request_id=?`, requestID); err != nil {
			return err
		}
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	if deliveryErr == nil && err == nil {
		delete(t.approvals, requestID)
	} else {
		pending.busy = false
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if deliveryErr != nil {
		return controlError(409, "Could not confirm delivery of the permission decision. Refresh or stop the owning conversation before retrying.")
	}
	if grantID != "" {
		a.resolveRememberedPermissions()
	}
	return nil
}

func validSQLQuestionAnswers(req QuestionRequest, answers [][]string, cancelled bool) bool {
	if cancelled {
		return len(answers) == 0
	}
	if len(answers) != len(req.Items) {
		return false
	}
	for i, q := range req.Items {
		if len(answers[i]) == 0 || len(answers[i]) > 65 || !q.Multiple && len(answers[i]) != 1 {
			return false
		}
		seen := map[string]bool{}
		for _, answer := range answers[i] {
			if strings.TrimSpace(answer) == "" || len(answer) > 16384 || !utf8.ValidString(answer) || strings.ContainsRune(answer, 0) || seen[answer] {
				return false
			}
			seen[answer] = true
			if !q.Custom && !slices.ContainsFunc(q.Options, func(o QuestionOption) bool { return o.Label == answer }) {
				return false
			}
		}
	}
	return true
}

func (a *app) replySQLQuestion(id, requestID string, answers [][]string, cancelled bool, origin *SpawnOrigin) error {
	a.mu.Lock()
	var err error
	id, err = a.sqlRequestOwnerLocked(id, requestID, "question", origin)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	t := a.runs[id]
	if t == nil || t.ctx.Err() != nil || t.questions[requestID] == nil {
		a.mu.Unlock()
		return controlError(409, "This question is no longer waiting for an answer.")
	}
	pending := t.questions[requestID]
	if pending.request.Elicitation != nil {
		a.mu.Unlock()
		return controlError(400, "MCP elicitation requires an explicit human form response.")
	}
	if pending.busy {
		a.mu.Unlock()
		return controlError(409, "This answer is already being sent.")
	}
	if !validSQLQuestionAnswers(pending.request, answers, cancelled) {
		a.mu.Unlock()
		return controlError(400, "Answer every question using the offered choices or an allowed custom answer.")
	}
	err = a.writeSQLLocked(t.ctx, func(st *sqliteSessionTx) error {
		key, err := st.requestKey("pending_questions", id, requestID)
		if err != nil {
			return err
		}
		q := pending.request
		q.Resolving = true
		return st.putExistingRecord("pending_questions", key, q)
	})
	if err != nil {
		a.mu.Unlock()
		return err
	}
	pending.busy = true
	a.mu.Unlock()
	err = t.ctx.Err()
	if err == nil {
		err = pending.reply(answers, cancelled)
	}
	deliveryErr := err
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		key, err := st.requestKey("pending_questions", id, requestID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if deliveryErr != nil {
			if key != "" {
				q := pending.request
				q.Resolving = false
				return st.putExistingRecord("pending_questions", key, q)
			}
			return nil
		}
		if key != "" {
			if err := st.deleteRecord("pending_questions", key); err != nil {
				return err
			}
		}
		entry := event("status", "Question dismissed.")
		entry.Status = "cancelled"
		if !cancelled {
			parts := []string{}
			for i, q := range pending.request.Items {
				answer := strings.Join(answers[i], ", ")
				if q.Secret {
					answer = "[Private answer sent]"
				}
				parts = append(parts, q.Text+"\n\n"+answer)
			}
			entry = event("user", strings.Join(parts, "\n\n"))
			if origin != nil {
				entry.Type, entry.Data = "agent_prompt", map[string]any{"origin": origin}
			}
		}
		entry.Title, entry.ConsultationID = "Question response", t.consultationID
		if _, err := st.appendEvent(id, entry); err != nil {
			return err
		}
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if entry.Type == "user" && s.Role == sessionRoleGeneralAgent {
			s.HarnessLocked = true
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	if deliveryErr == nil && err == nil {
		delete(t.questions, requestID)
	} else {
		pending.busy = false
	}
	if err != nil {
		return err
	}
	if deliveryErr != nil {
		return controlError(409, "Could not deliver the answer. Refresh or stop the owning conversation.")
	}
	return nil
}

func (a *app) resolveSQLPermissions() {
	type resolution struct {
		id, request, decision, reason string
		owner                         *turn
		pending                       *pendingApproval
	}
	ready := []resolution{}
	a.mu.Lock()
	for id, t := range a.runs {
		if t.ctx.Err() != nil || a.storageErr != nil {
			continue
		}
		err := a.store.readSnapshot(t.ctx, func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: t.ctx, tx: tx}
			s, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			yolo, err := st.sessionYOLO(s)
			if err != nil {
				return err
			}
			for request, p := range t.approvals {
				if p.busy {
					continue
				}
				decision, err := st.remembered(s, p.ruleHarness, p.ruleKind, p.ruleKey)
				if err != nil {
					return err
				}
				if yolo {
					decision = "allow"
				}
				if decision == "" || decision == "allow" && !slices.Contains(p.request.Decisions, "once") {
					continue
				}
				reason := "saved rule"
				if yolo {
					reason = "YOLO mode"
				}
				p.busy = true
				ready = append(ready, resolution{id: id, request: request, decision: decision, reason: reason, owner: t, pending: p})
			}
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			a.failStorageLocked(err)
			break
		}
	}
	a.mu.Unlock()
	for _, r := range ready {
		err := r.owner.ctx.Err()
		if err == nil {
			err = r.pending.reply(r.decision == "allow")
		}
		a.mu.Lock()
		if err != nil {
			r.pending.busy = false
			a.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
			key, err := st.requestKey("pending_permissions", r.id, r.request)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if key != "" {
				if err := st.deleteRecord("pending_permissions", key); err != nil {
					return err
				}
			}
			e := event("status", "Automatic permission decision: "+r.decision+" ("+r.reason+")")
			e.Title, e.Status, e.ConsultationID = r.pending.request.Title, "completed", r.owner.consultationID
			e.Data = map[string]any{"permission": r.pending.request.Kind, "decision": r.decision, "automatic": true, "scope": r.pending.request.ScopeLabel, "reason": r.reason}
			if _, err := st.appendEvent(r.id, e); err != nil {
				return err
			}
			s, err := st.sessionMeta(r.id)
			if err != nil {
				return err
			}
			s.UpdatedAt = now()
			return st.putSessionMeta(s)
		})
		cancel()
		if err == nil {
			delete(r.owner.approvals, r.request)
		} else {
			r.pending.busy = false
		}
		a.mu.Unlock()
	}
}
