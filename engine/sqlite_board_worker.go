package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (a *app) readSQLBoardConfig(ctx context.Context) (activityBoardConfig, error) {
	var c activityBoardConfig
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		c, err = st.boardConfig()
		return err
	})
	return c, err
}
func (a *app) startSQLActivityBoardLocked() error {
	err := a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		migrated := false
		if err := st.eachMatching("board_connections", "1", nil, func(id string) error {
			c, err := st.boardConnection(id)
			if err != nil {
				return err
			}
			sync, err := st.boardSync(id)
			if err != nil {
				return err
			}
			if c.Provider == "azure" && !c.OnlyAssignedToMe {
				c.OnlyAssignedToMe = true
				sync.SnapshotPending = true
				migrated = true
				if err := st.putRoot("board_connections", id, c); err != nil {
					return err
				}
			}
			sync.Stale = true
			return st.putRoot("board_sync", id, sync)
		}); err != nil {
			return err
		}
		if err := st.eachMatching("board_moves", `status='pending'`, nil, func(id string) error {
			var m activityBoardMove
			if err := decodeSQLiteRecord(st.ctx, st.tx, "board_moves", id, 64<<10, &m); err != nil {
				return err
			}
			m.Status, m.Message, m.UpdatedAt = "unconfirmed", "Update interrupted by engine restart. Refresh to reconcile; the write was not replayed.", now()
			return st.putRoot("board_moves", id, m)
		}); err != nil {
			return err
		}
		if err := st.pruneBoardMoves(); err != nil {
			return err
		}
		return st.boardProjection(migrated)
	})
	if err != nil {
		return err
	}
	after := ""
	for {
		ids, err := sqliteQueryIDs(a.ctx, a.store.readers, `SELECT id FROM data_board_connections WHERE id>? ORDER BY id LIMIT 100`, after)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			a.scheduleSQLBoardRefreshLocked(id)
			after = id
		}
	}
}
func (a *app) scheduleSQLBoardRefreshLocked(id string) {
	if a.closing || a.storageErr != nil || a.activityBoardBusy[id] {
		return
	}
	var c activityBoardConnection
	var protected []byte
	var revision uint64
	err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
		var err error
		c, err = st.boardConnection(id)
		if err != nil {
			return err
		}
		available, err := st.projectAvailable(c.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		revision = config.Revision
		return decodeSQLiteRecord(st.ctx, tx, "board_credentials", id, 1<<20, &protected)
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && a.ctx.Err() == nil {
			a.failStorageLocked(err)
		}
		return
	}
	if a.activityBoardBusy == nil {
		a.activityBoardBusy = map[string]bool{}
	}
	if a.activityBoardSlots == nil {
		a.activityBoardSlots = make(chan struct{}, 2)
	}
	a.activityBoardBusy[id] = true
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
		token, scanErr := boardCredential(protected, false)
		resolved := c
		var items map[string]activityBoardItem
		if scanErr == nil {
			var values []activityBoardValue
			resolved, values, scanErr = boardDiscover(ctx, c, string(token))
			if scanErr == nil {
				scanErr = boardResolveMappings(a.boardPhasesSnapshot(revision), resolved.Mappings, values)
			}
			if scanErr == nil {
				items, scanErr = boardScan(ctx, resolved, string(token))
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.activityBoardBusy, id)
		if a.closing || a.storageErr != nil {
			return
		}
		_ = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
			config, err := st.boardConfig()
			if err != nil {
				return err
			}
			available, err := st.projectAvailable(c.ProjectID)
			if err != nil {
				return err
			}
			if config.Revision != revision || !available {
				return nil
			}
			if scanErr == nil {
				for key, item := range items {
					var old activityBoardItem
					err := decodeSQLiteRecord(st.ctx, st.tx, "board_items", key, 2<<20, &old)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if err == nil && old.ConnectionID != id {
						scanErr = errors.New("Conflicting provider identity across board scopes. Refresh the previous scope first.")
						break
					}
					if err == nil && c.Provider == "azure" && old.Revision > item.Revision {
						items[key] = old
					}
				}
			}
			if scanErr != nil {
				sync, err := st.boardSync(id)
				if err != nil {
					return err
				}
				sync.Stale, sync.Error = true, scanErr.Error()
				if err := st.putRoot("board_sync", id, sync); err != nil {
					return err
				}
				return st.boardProjection(false)
			}
			if err := st.eachMatching("board_items", `x_connectionId=?`, []any{id}, func(key string) error {
				if _, keep := items[key]; keep {
					return nil
				}
				return st.deleteRecord("board_items", key)
			}); err != nil {
				return err
			}
			for _, item := range items {
				if err := st.putBoardItem(item, resolved); err != nil {
					return err
				}
			}
			if err := st.putRoot("board_connections", id, resolved); err != nil {
				return err
			}
			if err := st.putRoot("board_sync", id, activityBoardSync{LastSyncedAt: now()}); err != nil {
				return err
			}
			if err := st.eachMatching("board_moves", `x_connectionId=? AND status IN ('pending','unconfirmed')`, []any{id}, func(key string) error {
				var m activityBoardMove
				if err := decodeSQLiteRecord(st.ctx, st.tx, "board_moves", key, 64<<10, &m); err != nil {
					return err
				}
				item, ok := items[m.Key]
				if !ok {
					return nil
				}
				m.Status, m.Message = "failed", "Provider state reconciled. The requested phase is not current; a new explicit move is required."
				if boardClassify(item, resolved) == m.PhaseID {
					m.Status, m.Message = "confirmed", "Requested phase observed in provider after reconciliation."
				}
				m.UpdatedAt = now()
				return st.putRoot("board_moves", key, m)
			}); err != nil {
				return err
			}
			if err := st.pruneBoardMoves(); err != nil {
				return err
			}
			return st.boardProjection(false)
		})
	}()
}
