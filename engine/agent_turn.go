package main

import (
	"context"
	"database/sql"
	"errors"
)

func (a *app) prepareAgentTurn(ctx context.Context, id string, frozen *AgentExecutionCapture) (*AgentTurnPreparation, error) {
	if frozen != nil {
		if frozen.ExternalCapabilities == nil && (managedLibraryHarness(frozen.Config.Harness) || !selectionEmpty(frozen.Config.Selection)) {
			return nil, errors.New("Frozen agent execution requires its external capability capture, including an explicit empty managed selection.")
		}
		capture := AgentCapture{}
		if frozen.Source != nil {
			capture = *frozen.Source
		}
		return &AgentTurnPreparation{Resolution: AgentResolution{Config: frozen.Config, Capture: capture, SourceStatus: "captured", Token: frozen.Revision}, ExternalCapabilities: frozen.ExternalCapabilities}, nil
	}
	s, binding, cwd, err := a.readBoundAgent(ctx, id)
	if err != nil || binding == nil {
		return nil, err
	}
	if prepared, active, err := a.prepareAutomationAgent(ctx, s); active || err != nil {
		return prepared, err
	}
	res, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: s.ProjectID, ExecutionCWD: cwd})
	if err != nil {
		return nil, err
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	if res.Blocked == nil && res.SourceStatus == "linked" {
		if err := a.recheckBoundAgentLocked(ctx, res, s.ProjectID); err != nil {
			return nil, err
		}
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	var external *CapabilitySnapshot
	if res.Blocked == nil {
		external, err = a.captureCapabilities(ctx, res.Config.Selection, s.Harness)
		if err != nil {
			res.Blocked = agentProblem(409, "capabilities_unavailable", "selection", err.Error())
		}
	}
	updated := cloneAgentBinding(*binding)
	updated.Revision, err = nextAgentRevision(binding.Revision)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	var selectionRevision string
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		current, err := st.agentBinding(id)
		if err != nil {
			return err
		}
		if current == nil || current.Revision != binding.Revision {
			return agentProblem(409, "binding_changed", "revision", "Agent settings changed during turn preparation. Retry.")
		}
		var pending bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return errors.New("Library deletion needs recovery before preparing a turn.")
		}
		if err := st.saveAgentConfiguration(id, updated, res, false); err != nil {
			return err
		}
		var selection ConversationCapabilities
		if err := decodeSQLiteRecord(ctx, st.tx, "session_capabilities", sqliteOwnedKey(id, ""), 64<<10, &selection); err != nil {
			return err
		}
		selectionRevision = sqliteRevision(selection.Revision)
		if external != nil {
			return st.putSingleChild("capability_preparing", sqliteOwnedKey(id, ""), external)
		}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if res.Blocked != nil {
		return nil, res.Blocked
	}
	return &AgentTurnPreparation{Resolution: res, BindingRevision: updated.Revision, SelectionRevision: selectionRevision, ExternalCapabilities: external}, nil
}

func (st *sqliteSessionTx) markAgentApplied(id string, preparation *AgentTurnPreparation) error {
	if preparation == nil {
		return nil
	}
	binding, err := st.agentBinding(id)
	if err != nil || binding == nil {
		return err
	}
	capture := preparation.Resolution.Capture
	binding.Applied = &AgentExecutionCapture{Config: preparation.Resolution.Config, Source: &capture, Revision: agentConfigDigest(preparation.Resolution.Config)}
	binding.Revision, err = nextAgentRevision(binding.Revision)
	if err != nil {
		return err
	}
	res := AgentResolution{Config: binding.Desired, Capture: binding.LastCaptured, SourceStatus: binding.SourceStatus, Blocked: binding.Blocked, Token: binding.Revision}
	return st.saveAgentConfiguration(id, *binding, res, false)
}

// The scheduler reserves a turn but leaves queued payloads intact until one
// specialist/model/Library preparation succeeds. A failed preparation pauses the
// exact queue, rather than consuming it or starting an automatic retry loop.
func (a *app) executeBoundQueued(t *turn, s Session, native nativeSession, b binary, cwd string, payload submission, messages []QueuedMessage) {
	preparation, err := a.prepareAgentTurn(t.ctx, s.ID, nil)
	a.mu.Lock()
	if a.runs[s.ID] != t || t.ctx.Err() != nil {
		err = context.Canceled
	}
	if err == nil {
		err = a.writeSQLLocked(t.ctx, func(st *sqliteSessionTx) error {
			current, err := st.sessionMeta(s.ID)
			if err != nil {
				return err
			}
			binding, err := st.agentBinding(s.ID)
			if err != nil {
				return err
			}
			if binding == nil || binding.Revision != preparation.BindingRevision {
				return errors.New("Agent settings changed before queued execution.")
			}
			for _, q := range messages {
				key, err := st.queueKey(s.ID, q.ID)
				if err != nil {
					return err
				}
				var pending QueuedMessage
				if err := decodeSQLiteRecord(st.ctx, st.tx, "queued_messages", key, 1<<20, &pending); err != nil {
					return err
				}
				if pending.Status != "queued" && pending.Status != "steering" {
					return errors.New("Queued message changed during agent preparation.")
				}
				if err := st.appendQueuedUser(s.ID, q); err != nil {
					return err
				}
			}
			current.Status = "running"
			current.UpdatedAt = now()
			for _, table := range []string{"pending_permissions", "pending_questions"} {
				if err := st.clearChildren(table, s.ID); err != nil {
					return err
				}
			}
			if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO runtime_activity(session_id,boundary) SELECT ?,size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events' ON CONFLICT(session_id) DO UPDATE SET boundary=excluded.boundary,reasoning_title=''`, s.ID, s.ID); err != nil {
				return err
			}
			return st.putSessionMeta(current)
		})
	}
	if err != nil {
		_ = a.persistSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
			_, saveErr := st.recoverQueue(s.ID, err.Error())
			if saveErr != nil {
				return saveErr
			}
			if err := st.putSingleChild("capability_preparing", sqliteOwnedKey(s.ID, ""), nil); err != nil {
				return err
			}
			meta, saveErr := st.sessionMeta(s.ID)
			if saveErr != nil {
				return saveErr
			}
			meta.Status = "error"
			if errors.Is(err, context.Canceled) || t.ctx.Err() != nil {
				meta.Status = "idle"
			}
			return st.putSessionMeta(meta)
		})
		if a.runs[s.ID] == t {
			delete(a.runs, s.ID)
		}
		a.mu.Unlock()
		t.cancel()
		close(t.done)
		a.wg.Done()
		return
	}
	a.mu.Unlock()
	a.execute(t, s, native, b, cwd, payload, preparation)
}

func (a *app) agentBindingExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		binding, err := (&sqliteSessionTx{ctx: ctx, tx: tx}).agentBinding(id)
		exists = binding != nil
		return err
	})
	return exists, err
}

// Kept as a read helper so admission never performs filesystem/catalog work in
// an application lock or a SQL transaction.
func (a *app) checkAgentMessageEligibility(ctx context.Context, id string) error {
	s, binding, cwd, err := a.readBoundAgent(ctx, id)
	if err != nil || binding == nil {
		return err
	}
	res, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: s.ProjectID, ExecutionCWD: cwd})
	if err != nil {
		return err
	}
	if res.Blocked != nil {
		return res.Blocked
	}
	return nil
}
