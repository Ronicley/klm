package main

import (
	"context"
	"errors"
	"time"
)

func pruneBoardMoves(d *diskState) {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for id, m := range d.ActivityBoardMoves {
		t, err := time.Parse(time.RFC3339Nano, m.UpdatedAt)
		if !boardMoveUnresolved(m) && err == nil && t.Before(cutoff) {
			delete(d.ActivityBoardMoves, id)
		}
	}
}
func (a *app) startActivityBoard() error {
	a.activityBoardBusy = map[string]bool{}
	a.activityBoardSlots = make(chan struct{}, 2)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.state.ActivityBoardConnections) == 0 {
		return nil
	}
	if err := a.commitTransactionLocked(func(d *diskState) error {
		migrated := false
		for id, c := range d.ActivityBoardConnections {
			if c.Provider == "azure" && !c.OnlyAssignedToMe {
				c.OnlyAssignedToMe = true
				d.ActivityBoardConnections[id] = c
				s := d.ActivityBoardSync[id]
				s.SnapshotPending = true
				d.ActivityBoardSync[id] = s
				migrated = true
			}
		}
		if migrated {
			d.ActivityBoardConfig.Revision++
			d.ActivityBoardConfig.Projection++
		}
		for id, s := range d.ActivityBoardSync {
			s.Stale = true
			d.ActivityBoardSync[id] = s
		}
		for id, m := range d.ActivityBoardMoves {
			if m.Status == "pending" {
				m.Status = "unconfirmed"
				m.Message = "Update interrupted by engine restart. Refresh to reconcile; the write was not replayed."
				m.UpdatedAt = now()
				d.ActivityBoardMoves[id] = m
			}
		}
		pruneBoardMoves(d)
		return nil
	}); err != nil {
		return err
	}
	for id := range a.state.ActivityBoardConnections {
		a.boardScheduleRefreshLocked(id)
	}
	return nil
}
func (a *app) boardScheduleRefreshLocked(id string) {
	c := a.state.ActivityBoardConnections[id]
	c.Mappings = cloneTransactionValueOfMappings(c.Mappings)
	if a.closing || a.storageErr != nil || a.activityBoardBusy[id] || !a.boardAvailableProject(c.ProjectID) {
		return
	}
	a.activityBoardBusy[id] = true
	revision := a.state.ActivityBoardConfig.Revision
	protected := a.state.ActivityBoardCredentials[id]
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		select {
		case a.activityBoardSlots <- struct{}{}:
			defer func() { <-a.activityBoardSlots }()
		case <-ctx.Done():
			a.mu.Lock()
			delete(a.activityBoardBusy, id)
			a.mu.Unlock()
			return
		}
		token, err := boardCredential(protected, false)
		var items map[string]activityBoardItem
		resolved := c
		if err == nil {
			var values []activityBoardValue
			resolved, values, err = boardDiscover(ctx, c, string(token))
			if err == nil {
				err = boardResolveMappings(a.boardPhasesSnapshot(revision), resolved.Mappings, values)
			}
			if err == nil {
				items, err = boardScan(ctx, resolved, string(token))
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.activityBoardBusy, id)
		if a.closing || a.storageErr != nil || a.state.ActivityBoardConfig.Revision != revision || !a.boardAvailableProject(c.ProjectID) {
			return
		}
		if err == nil {
			for key, item := range items {
				if other, ok := a.state.ActivityBoardItems[key]; ok && other.ConnectionID != id {
					err = errors.New("Conflicting provider identity across board scopes. Refresh the previous scope first.")
					break
				}
				if c.Provider == "azure" {
					if old, ok := a.state.ActivityBoardItems[key]; ok && old.Revision > item.Revision {
						items[key] = old
					}
				}
			}
		}
		_ = a.commitTransactionLocked(func(d *diskState) error {
			s := d.ActivityBoardSync[id]
			if err != nil {
				s.Stale = true
				s.Error = err.Error()
				d.ActivityBoardSync[id] = s
				d.ActivityBoardConfig.Projection++
				return nil
			}
			// Only a complete scan retires missing items. No partial batch is published.
			for key, item := range d.ActivityBoardItems {
				if item.ConnectionID == id {
					delete(d.ActivityBoardItems, key)
				}
			}
			for key, item := range items {
				d.ActivityBoardItems[key] = item
			}
			d.ActivityBoardConnections[id] = resolved
			d.ActivityBoardSync[id] = activityBoardSync{LastSyncedAt: now(), Stale: false}
			for op, m := range d.ActivityBoardMoves {
				if m.ConnectionID != id || !boardMoveUnresolved(m) {
					continue
				}
				item, ok := items[m.Key]
				if !ok {
					continue
				}
				m.Status = "failed"
				m.Message = "Provider state reconciled. The requested phase is not current; a new explicit move is required."
				if boardClassify(item, resolved) == m.PhaseID {
					m.Status = "confirmed"
					m.Message = "Requested phase observed in provider after reconciliation."
				}
				m.UpdatedAt = now()
				d.ActivityBoardMoves[op] = m
			}
			pruneBoardMoves(d)
			d.ActivityBoardConfig.Projection++
			return nil
		})
	}()
}
func (a *app) boardPhasesSnapshot(revision uint64) []activityBoardPhase {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.ActivityBoardConfig.Revision != revision {
		return nil
	}
	return append([]activityBoardPhase{}, a.state.ActivityBoardConfig.Phases...)
}
func (a *app) boardExecuteMove(c activityBoardConnection, protected []byte, item activityBoardItem, m activityBoardMove) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	actual := item
	token, err := boardCredential(protected, false)
	status, message := "failed", ""
	sent := false
	if err == nil {
		var catalog []activityBoardValue
		c, catalog, err = boardDiscover(ctx, c, string(token))
		if err == nil {
			err = boardResolveMappings(a.boardPhasesSnapshot(m.ConfigRevision), c.Mappings, catalog)
		}
	}
	if err == nil {
		actual, err = boardReadItem(ctx, c, string(token), item)
	}
	if err == nil && actual.Version != m.ObservedVersion {
		err = errors.New("Item changed. Refresh and try again.")
	}
	// Project removal while doing HTTP must prevent a not-yet-dispatched write.
	a.mu.Lock()
	available := !a.closing && a.boardAvailableProject(c.ProjectID) && a.state.ActivityBoardConfig.Revision == m.ConfigRevision
	a.mu.Unlock()
	if err == nil && !available {
		err = errors.New("Board scope unavailable. Refresh before another update.")
	}
	if err == nil && boardClassify(actual, c) == m.PhaseID {
		status = "confirmed"
		message = "Phase confirmed in provider."
	} else if err == nil {
		sent = true
		if c.Provider == "github" {
			err = boardMoveGithub(ctx, c, string(token), actual, m.PhaseID)
		} else {
			err = boardMoveAzure(ctx, c, string(token), actual, m.PhaseID)
		}
		writeErr := err
		// Always try an authoritative read after a sent mutation, including partial failures.
		read, readErr := boardReadItem(ctx, c, string(token), actual)
		if readErr == nil {
			actual = read
		}
		if writeErr == nil && readErr == nil && boardClassify(actual, c) == m.PhaseID {
			status = "confirmed"
			message = "Phase confirmed in provider."
		} else {
			status = "unconfirmed"
			message = "Update unconfirmed. Refresh to reconcile before another update."
			if writeErr != nil {
				message = writeErr.Error() + " Update unconfirmed; refresh to reconcile."
			} else if readErr == nil {
				status = "failed"
				message = "Provider did not retain the requested phase. Refresh and check concurrent changes."
			}
			// Azure's single revision-guarded PATCH can reject without partial label effects.
			var pe *boardProviderError
			if c.Provider == "azure" && errors.As(writeErr, &pe) && (pe.status == 400 || pe.status == 401 || pe.status == 403 || pe.status == 409 || pe.status == 412 || pe.status == 422) {
				status = "failed"
				message = pe.Error()
			}
		}
	}
	if !sent && err != nil {
		message = err.Error()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.activityBoardBusy, c.ID)
	_ = a.commitTransactionLocked(func(d *diskState) error {
		m.Status, m.Message, m.UpdatedAt = status, message, now()
		d.ActivityBoardMoves[m.OperationID] = m
		old := d.ActivityBoardItems[actual.Key]
		if actual.Version != "" && (c.Provider != "azure" || actual.Revision >= old.Revision) {
			d.ActivityBoardItems[actual.Key] = actual
		}
		if status != "confirmed" {
			s := d.ActivityBoardSync[c.ID]
			s.Stale = true
			s.Error = message
			d.ActivityBoardSync[c.ID] = s
		}
		d.ActivityBoardConfig.Projection++
		return nil
	})
}
