package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func (a *app) delegationRoutes(mux *http.ServeMux) {
	if a.store == nil {
		return
	}
	mux.HandleFunc("GET /api/sessions/{id}/delegation/batches", a.chatSessionHandler(a.delegationHTTP))
	mux.HandleFunc("GET /api/sessions/{id}/delegation/batches/{batchId}/members", a.chatSessionHandler(a.delegationHTTP))
	mux.HandleFunc("GET /api/sessions/{id}/delegation/batches/{batchId}/invocations/{invocationId}/output", a.chatSessionHandler(a.delegationHTTP))
	mux.HandleFunc("GET /api/sessions/{id}/delegation/requests", a.chatSessionHandler(a.delegationRequestsHTTP))
	mux.HandleFunc("GET /api/sessions/{id}/delegation/children", a.chatSessionHandler(a.delegationChildrenHTTP))
	for _, suffix := range []string{"batches", "batches/{batchId}/members", "batches/{batchId}/invocations/{invocationId}/output"} {
		mux.HandleFunc("GET /api/sessions/{id}/graph/runs/{runId}/activations/{activationId}/delegation/"+suffix, a.graphDelegationHTTP)
	}
}

func (a *app) delegationHTTP(w http.ResponseWriter, r *http.Request) {
	limit, offset := 20, 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if raw := r.URL.Query().Get("offset"); raw != "" && err == nil {
		offset, err = strconv.Atoi(raw)
	}
	if err != nil || limit < 1 || limit > 50 || offset < 0 {
		fail(w, 400, "Invalid delegation page bounds.")
		return
	}
	var result any
	if invocationID := r.PathValue("invocationId"); invocationID != "" {
		start, parseErr := delegationDecimalOffset(r.URL.Query().Get("offset"))
		if parseErr != nil {
			fail(w, 400, parseErr.Error())
			return
		}
		result, err = a.readDelegationOutput(r.Context(), r.PathValue("id"), r.PathValue("batchId"), invocationID, r.URL.Query().Get("digest"), start, 16<<10, r.PathValue("activationId"))
	} else if batchID := r.PathValue("batchId"); batchID != "" {
		result, err = a.readDelegationMembers(r.Context(), r.PathValue("id"), batchID, offset, limit, r.PathValue("activationId"))
	} else {
		result, err = a.readDelegationBatches(r.Context(), r.PathValue("id"), offset, limit, r.PathValue("activationId"))
	}
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, result)
}

type DelegationRequestView struct {
	OwnerSessionID string          `json:"ownerSessionId"`
	ChildSessionID string          `json:"childSessionId"`
	BatchID        string          `json:"batchId"`
	InvocationID   string          `json:"invocationId"`
	Kind           string          `json:"kind"`
	Request        json.RawMessage `json:"request"`
	ChildTitle     string          `json:"childTitle"`
}

// At most four live children, each with bounded pending requests; page by ID.
func (a *app) readDelegationRequests(ctx context.Context, owner, after string) ([]DelegationRequestView, bool, error) {
	items := []DelegationRequestView{}
	more := false
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		if _, err := st.delegationReadOwner(owner); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT kind,id,child,batch,invocation FROM (
 SELECT 'permission' kind,p.id,p.owner_id child,i.x_batchId batch,i.id invocation,p.record_id request FROM data_pending_permissions p JOIN data_delegation_invocations i ON i.x_childSessionId=p.owner_id JOIN data_delegation_batches b ON b.id=i.x_batchId WHERE b.x_ownerConversationId=? AND i.x_state<>'terminal'
 UNION ALL SELECT 'question',p.id,p.owner_id,i.x_batchId,i.id,p.record_id FROM data_pending_questions p JOIN data_delegation_invocations i ON i.x_childSessionId=p.owner_id JOIN data_delegation_batches b ON b.id=i.x_batchId WHERE b.x_ownerConversationId=? AND i.x_state<>'terminal') WHERE id>? ORDER BY id LIMIT 51`, owner, owner, after)
		if err != nil {
			return err
		}
		type ref struct{ kind, id, child, batch, invocation string }
		refs := []ref{}
		for rows.Next() {
			var v ref
			if err := rows.Scan(&v.kind, &v.id, &v.child, &v.batch, &v.invocation); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, v)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		more = len(refs) > 50
		if more {
			refs = refs[:50]
		}
		for _, v := range refs {
			table := "pending_permissions"
			if v.kind == "question" {
				table = "pending_questions"
			}
			var raw json.RawMessage
			if err := decodeSQLiteRecord(ctx, tx, table, v.id, 256<<10, &raw); err != nil {
				return err
			}
			child, err := st.sessionMeta(v.child)
			if err != nil {
				return err
			}
			items = append(items, DelegationRequestView{OwnerSessionID: owner, ChildSessionID: v.child, BatchID: v.batch, InvocationID: v.invocation, Kind: v.kind, Request: raw, ChildTitle: child.Title})
		}
		return nil
	})
	return items, more, err
}

func (a *app) delegationRequestsHTTP(w http.ResponseWriter, r *http.Request) {
	items, more, err := a.readDelegationRequests(r.Context(), r.PathValue("id"), r.URL.Query().Get("afterId"))
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	next := ""
	if len(items) > 0 {
		last := items[len(items)-1]
		var request struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(last.Request, &request)
		next = sqliteOwnedKey(last.ChildSessionID, request.ID)
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextAfterId": next})
}

func delegationOutputTool() map[string]any {
	return agentTool("subagent_output_read", "Read bounded full output of one settled child owned by this parent. Use its batchId, invocationId and digest from the consolidated result; offset is a decimal string.", map[string]any{"batchId": agentString("Exact batch ID"), "invocationId": agentString("Exact invocation ID"), "digest": agentString("Output digest"), "offset": agentString("Decimal byte offset; defaults to 0")}, "batchId", "invocationId", "digest")
}

func (p *adapter) callDelegationOutput(ctx context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		BatchID      string `json:"batchId"`
		InvocationID string `json:"invocationId"`
		Digest       string `json:"digest"`
		Offset       string `json:"offset"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	offset, err := delegationDecimalOffset(args.Offset)
	if err != nil {
		return nil, err
	}
	activation := ""
	if binding := p.turn.graphDelegation; binding != nil {
		activation = binding.Scope.ActivationID
	}
	return p.app.readDelegationOutput(ctx, p.id, args.BatchID, args.InvocationID, args.Digest, offset, 16<<10, activation)
}

func (a *app) graphDelegationHTTP(w http.ResponseWriter, r *http.Request) {
	var parent string
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		run, err := st.graphRunRecord(r.PathValue("runId"))
		if err != nil {
			return err
		}
		if run.ConversationID != r.PathValue("id") {
			return sql.ErrNoRows
		}
		if _, err := st.delegationReadOwner(run.ConversationID); err != nil {
			return err
		}
		x, err := st.graphActivation(r.PathValue("activationId"))
		if err != nil {
			return err
		}
		if x.RunID != run.ID || x.SessionID == "" {
			return sql.ErrNoRows
		}
		parent = x.SessionID
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	r.SetPathValue("id", parent)
	a.delegationHTTP(w, r)
}

type DelegationOwnedChildView struct {
	ID                 string `json:"id"`
	ParentSessionID    string `json:"parentSessionId"`
	Title              string `json:"title"`
	Harness            string `json:"harness"`
	ExecutionCWD       string `json:"executionCwd"`
	ActiveInvocationID string `json:"activeInvocationId,omitempty"`
	Quarantined        bool   `json:"quarantined"`
	Reusable           bool   `json:"reusable"`
	ReadOnly           bool   `json:"readOnly"`
}

func (a *app) delegationChildrenHTTP(w http.ResponseWriter, r *http.Request) {
	items := []DelegationOwnedChildView{}
	more := false
	owner := r.PathValue("id")
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if _, err := st.delegationReadOwner(owner); err != nil {
			return err
		}
		ids, err := sqliteQueryIDs(r.Context(), tx, `SELECT id FROM data_delegation_children WHERE x_parentSessionId=? AND id>? ORDER BY id LIMIT 51`, owner, r.URL.Query().Get("afterId"))
		if err != nil {
			return err
		}
		more = len(ids) > 50
		if more {
			ids = ids[:50]
		}
		for _, id := range ids {
			child, err := st.delegationChild(id)
			if err != nil {
				return err
			}
			meta, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			items = append(items, DelegationOwnedChildView{ID: id, ParentSessionID: owner, Title: meta.Title, Harness: child.Harness, ExecutionCWD: child.ExecutionCWD, ActiveInvocationID: child.ActiveInvocationID, Quarantined: child.Quarantined, Reusable: child.Reusable(), ReadOnly: true})
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	next := ""
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextAfterId": next})
}
