package main

import (
	"context"
	"database/sql"
	"errors"
)

type DelegationActions struct {
	Send      bool `json:"send"`
	Queue     bool `json:"queue"`
	Steer     bool `json:"steer"`
	Interrupt bool `json:"interrupt"`
	Settings  bool `json:"settings"`
	Stop      bool `json:"stop"`
}

type DelegationSessionStatus struct {
	Availability DelegationAvailability `json:"availability"`
	Waiting      bool                   `json:"waiting"`
	Actions      DelegationActions      `json:"actions"`
}

func (st *sqliteSessionTx) delegationSessionStatus(owner Session) (*DelegationSessionStatus, error) {
	if owner.Role == sessionRoleSubagent || owner.Role == "graph_node" {
		return nil, nil
	}
	native, err := st.native(owner.ID)
	if err != nil {
		return nil, err
	}
	archived, err := st.effectivelyArchived(owner)
	if err != nil {
		return nil, err
	}
	automation, err := st.activeAutomation(owner.ID)
	if err != nil {
		return nil, err
	}
	view := delegationBatchView(DelegationBatchRecord{State: DelegationSettled, Delivery: DelegationDeliveryDelivered}, nil, owner, archived, automation != nil)
	batch, err := st.activeDelegation(owner.ID)
	if err != nil {
		return nil, err
	}
	if batch != nil {
		view, err = st.delegationProgress(*batch, owner)
		if err != nil {
			return nil, err
		}
	}
	return &DelegationSessionStatus{Availability: delegationAvailability(owner.Role, owner.Harness, native.ID, native.Delegation), Waiting: view.Waiting, Actions: view.Actions}, nil
}

type DelegationBatchView struct {
	ID              string               `json:"id"`
	ParentSessionID string               `json:"parentSessionId"`
	Revision        string               `json:"revision"`
	State           DelegationBatchState `json:"state"`
	Total           int                  `json:"total"`
	Terminal        int                  `json:"terminal"`
	Remaining       int                  `json:"remaining"`
	Waiting         bool                 `json:"waiting"`
	Delivery        DelegationDelivery   `json:"delivery"`
	Outcome         DelegationOutcome    `json:"outcome,omitempty"`
	Actions         DelegationActions    `json:"actions"`
}

type DelegationChildView struct {
	Key                 string                     `json:"key"`
	BatchID             string                     `json:"batchId"`
	InvocationID        string                     `json:"invocationId"`
	ChildSessionID      string                     `json:"childSessionId"`
	ParentSessionID     string                     `json:"parentSessionId"`
	OwnerConversationID string                     `json:"ownerConversationId"`
	Title               string                     `json:"title"`
	Harness             string                     `json:"harness"`
	Model               string                     `json:"model"`
	Effort              string                     `json:"effort"`
	AgentID             string                     `json:"agentId,omitempty"`
	AgentName           string                     `json:"agentName,omitempty"`
	ExecutionState      DelegationInvocationState  `json:"executionState"`
	Outcome             DelegationOutcome          `json:"outcome,omitempty"`
	TaskOutcome         string                     `json:"taskOutcome,omitempty"`
	Terminal            bool                       `json:"terminal"`
	InvocationNumber    int64                      `json:"invocationNumber,string"`
	Reused              bool                       `json:"reused"`
	Error               string                     `json:"error,omitempty"`
	ResultReference     *DelegationResultReference `json:"resultReference,omitempty"`
}

type DelegationBatchPage struct {
	Items      []DelegationBatchView `json:"items"`
	Revision   string                `json:"revision"`
	HasMore    bool                  `json:"hasMore"`
	NextOffset int                   `json:"nextOffset"`
}

type DelegationMemberPage struct {
	Batch      DelegationBatchView   `json:"batch"`
	Items      []DelegationChildView `json:"items"`
	Revision   string                `json:"revision"`
	HasMore    bool                  `json:"hasMore"`
	NextOffset int                   `json:"nextOffset"`
}

func delegationBatchView(batch DelegationBatchRecord, members []DelegationInvocationRecord, owner Session, archived, automation bool) DelegationBatchView {
	view := DelegationBatchView{ID: batch.ID, ParentSessionID: batch.ParentSessionID, Revision: batch.Revision, State: batch.State, Total: batch.ExpectedChildren, Delivery: batch.Delivery, Outcome: batch.Outcome}
	for _, item := range members {
		if item.State == DelegationInvocationTerminal {
			view.Terminal++
		}
	}
	view.Remaining = view.Total - view.Terminal
	view.Waiting = batch.State != DelegationSettled || batch.Delivery == DelegationDeliveryPending || batch.Delivery == DelegationDeliverySending || batch.Delivery == DelegationDeliveryUncertain
	view.Actions = DelegationActions{Send: !archived, Queue: !archived, Steer: !archived && owner.Status == "running" && !view.Waiting, Interrupt: !archived && !automation, Settings: !archived && !automation && owner.Status != "running", Stop: true}
	return view
}

func (st *sqliteSessionTx) delegationView(batch DelegationBatchRecord, owner Session) (DelegationBatchView, []DelegationInvocationRecord, error) {
	members, err := st.delegationMembers(batch.ID)
	if err != nil {
		return DelegationBatchView{}, nil, err
	}
	archived, err := st.effectivelyArchived(owner)
	if err != nil {
		return DelegationBatchView{}, nil, err
	}
	automation, err := st.activeAutomation(owner.ID)
	if err != nil {
		return DelegationBatchView{}, nil, err
	}
	view := delegationBatchView(batch, members, owner, archived, automation != nil)
	err = st.delegationAliasWaiting(&view)
	return view, members, err
}

// Inventory/list progress does not hydrate result/prompt/capability payloads.
func (st *sqliteSessionTx) delegationProgress(batch DelegationBatchRecord, owner Session) (DelegationBatchView, error) {
	archived, err := st.effectivelyArchived(owner)
	if err != nil {
		return DelegationBatchView{}, err
	}
	automation, err := st.activeAutomation(owner.ID)
	if err != nil {
		return DelegationBatchView{}, err
	}
	view := delegationBatchView(batch, nil, owner, archived, automation != nil)
	if err := st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM data_delegation_invocations WHERE x_batchId=? AND x_state='terminal'`, batch.ID).Scan(&view.Terminal); err != nil {
		return view, err
	}
	view.Remaining = view.Total - view.Terminal
	err = st.delegationAliasWaiting(&view)
	return view, err
}

func (st *sqliteSessionTx) delegationAliasWaiting(view *DelegationBatchView) error {
	var pending bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_delegation_calls WHERE owner_id=? AND x_delivery IN ('pending','sending','uncertain'))`, view.ID).Scan(&pending); err != nil {
		return err
	}
	view.Waiting = view.Waiting || pending
	if view.Waiting {
		view.Actions.Steer, view.Actions.Settings = false, false
	}
	return nil
}

func (a *app) readDelegationBatches(ctx context.Context, ownerID string, offset, limit int, activations ...string) (DelegationBatchPage, error) {
	page := DelegationBatchPage{Items: []DelegationBatchView{}}
	if !delegationDomainsKnown() {
		return page, errors.New("Delegation storage is unavailable.")
	}
	if offset < 0 || limit < 1 || limit > 50 {
		return page, errors.New("Invalid delegation page.")
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		owner, err := st.delegationReadOwner(ownerID)
		if err != nil {
			return err
		}
		activation := ""
		if len(activations) > 0 {
			activation = activations[0]
		}
		ids, err := sqliteQueryIDs(ctx, tx, `SELECT id FROM data_delegation_batches WHERE x_parentSessionId=? AND x_graph_activationId=? ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, ownerID, activation, limit+1, offset)
		if err != nil {
			return err
		}
		page.HasMore = len(ids) > limit
		if page.HasMore {
			ids = ids[:limit]
		}
		for _, id := range ids {
			batch, err := st.delegationBatch(id)
			if err != nil {
				return err
			}
			if batch.Graph != nil && batch.Graph.ActivationID != activation {
				return errors.New("Ordinary delegation projection cannot expose graph ownership.")
			}
			view, err := st.delegationProgress(batch, owner)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, view)
		}
		page.NextOffset = offset + len(page.Items)
		return tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&page.Revision)
	})
	return page, err
}

func (a *app) readDelegationMembers(ctx context.Context, ownerID, batchID string, offset, limit int, activations ...string) (DelegationMemberPage, error) {
	page := DelegationMemberPage{Items: []DelegationChildView{}}
	if !delegationDomainsKnown() {
		return page, errors.New("Delegation storage is unavailable.")
	}
	if offset < 0 || limit < 1 || limit > 50 {
		return page, errors.New("Invalid delegation member page.")
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		owner, err := st.delegationReadOwner(ownerID)
		if err != nil {
			return err
		}
		batch, err := st.delegationBatch(batchID)
		if err != nil {
			return err
		}
		activation := ""
		if len(activations) > 0 {
			activation = activations[0]
		}
		if batch.ParentSessionID != ownerID || batch.Graph != nil && batch.Graph.ActivationID != activation || batch.Graph == nil && activation != "" {
			return sql.ErrNoRows
		}
		view, members, err := st.delegationView(batch, owner)
		if err != nil {
			return err
		}
		page.Batch = view
		end := min(len(members), offset+limit)
		for index := offset; index < end; index++ {
			item := members[index]
			child, err := st.sessionMeta(item.ChildSessionID)
			if err != nil {
				return err
			}
			entry := DelegationChildView{Key: item.Key, BatchID: batch.ID, InvocationID: item.ID, ChildSessionID: item.ChildSessionID, ParentSessionID: ownerID, OwnerConversationID: batch.OwnerConversationID, Title: child.Title, Harness: item.Capture.Config.Harness, Model: item.Capture.Config.Model, Effort: item.Capture.Config.Effort, ExecutionState: item.State, Outcome: item.Outcome, TaskOutcome: item.TaskOutcome, Terminal: item.State == DelegationInvocationTerminal, Error: delegationSummary(item.Error, 1024)}
			if item.Capture.Source != nil {
				entry.AgentID, entry.AgentName = item.Capture.Source.Source.ID, item.Capture.Source.Name
			}
			if item.State == DelegationInvocationTerminal {
				entry.ResultReference = &DelegationResultReference{InvocationID: item.ID, Digest: delegationOutputDigest(item.Result), TotalBytes: int64(len(item.Result))}
			}
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM data_delegation_invocations WHERE x_childSessionId=? AND (created_at<? OR (created_at=? AND id<=?))`, item.ChildSessionID, sqliteTimeKey(item.CreatedAt), sqliteTimeKey(item.CreatedAt), item.ID).Scan(&entry.InvocationNumber); err != nil {
				return err
			}
			entry.Reused = entry.InvocationNumber > 1
			page.Items = append(page.Items, entry)
		}
		page.HasMore, page.NextOffset = end < len(members), offset+len(page.Items)
		return tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&page.Revision)
	})
	return page, err
}
