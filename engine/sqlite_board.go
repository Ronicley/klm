package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (st *sqliteSessionTx) boardConfig() (activityBoardConfig, error) {
	var c activityBoardConfig
	err := decodeSQLiteRecord(st.ctx, st.tx, "board_config", "settings", 64<<10, &c)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if c.Schema == 0 {
		c.Schema = 1
	}
	c.Phases = []activityBoardPhase{}
	ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_board_phases WHERE owner_id='settings' ORDER BY position,id`)
	if err != nil {
		return c, err
	}
	for _, id := range ids {
		var p activityBoardPhase
		if err := decodeSQLiteRecord(st.ctx, st.tx, "board_phases", id, 64<<10, &p); err != nil {
			return c, err
		}
		c.Phases = append(c.Phases, p)
	}
	return c, nil
}
func (st *sqliteSessionTx) boardConnection(id string) (activityBoardConnection, error) {
	var c activityBoardConnection
	err := decodeSQLiteRecord(st.ctx, st.tx, "board_connections", id, 2<<20, &c)
	return c, err
}
func (st *sqliteSessionTx) boardSync(id string) (activityBoardSync, error) {
	var s activityBoardSync
	err := decodeSQLiteRecord(st.ctx, st.tx, "board_sync", id, 64<<10, &s)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return s, err
}
func (st *sqliteSessionTx) boardProjection(config bool) error {
	c, err := st.boardConfig()
	if err != nil {
		return err
	}
	if c.Projection == ^uint64(0) || config && c.Revision == ^uint64(0) {
		return errors.New("Board revision exhausted.")
	}
	c.Projection++
	if config {
		c.Revision++
	}
	c.Phases = nil
	return st.putRoot("board_config", "settings", c)
}
func (a *app) sqlBoardCheck(st *sqliteSessionTx, revision uint64, credentialOnly bool) error {
	if a.closing || a.storageErr != nil {
		return controlError(503, "Board persistence unavailable.")
	}
	c, err := st.boardConfig()
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return controlError(409, "Board configuration changed. Reload settings.")
	}
	if !credentialOnly {
		var pending bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_board_moves WHERE status IN ('pending','unconfirmed'))`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "Reconcile unconfirmed updates with Refresh before changing configuration.")
		}
	}
	for _, busy := range a.activityBoardBusy {
		if busy {
			return controlError(409, "Board is refreshing or updating. Wait before changing configuration.")
		}
	}
	return nil
}
func (st *sqliteSessionTx) putBoardItem(item activityBoardItem, c activityBoardConnection) error {
	if err := st.putRoot("board_items", item.Key, item); err != nil {
		return err
	}
	return indexSQLBoardItem(st.ctx, st.tx, item, c)
}
func indexSQLBoardItem(ctx context.Context, tx *sql.Tx, item activityBoardItem, c activityBoardConnection) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO board_item_views VALUES(?,?,?) ON CONFLICT(item_id) DO UPDATE SET phase=excluded.phase,sprint=excluded.sprint`, item.Key, boardClassify(item, c), boardSprintKey(c, item))
	return err
}
func (st *sqliteSessionTx) pruneBoardMoves() error {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	return st.eachMatching("board_moves", `status NOT IN ('pending','unconfirmed') AND updated_at<>'' AND updated_at<?`, []any{sqliteTimeKey(cutoff.UTC().Format(time.RFC3339Nano))}, func(id string) error {
		var m activityBoardMove
		if err := decodeSQLiteRecord(st.ctx, st.tx, "board_moves", id, 64<<10, &m); err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, m.UpdatedAt)
		if err != nil || !at.Before(cutoff) {
			return nil
		}
		return st.deleteRecord("board_moves", id)
	})
}
func (a *app) sqlBoardInputConnection(in boardConnectionInput, allowRetained bool) (activityBoardConnection, string, error) {
	c := activityBoardConnection{ID: in.ConnectionID, Name: in.Name, Provider: in.Provider, ProjectID: in.ProjectID, Scope: in.Scope, Mappings: in.Mappings}
	if in.ConnectionID != "" {
		var protected []byte
		err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
			var err error
			c, err = st.boardConnection(in.ConnectionID)
			if err != nil {
				return err
			}
			available, err := st.projectAvailable(c.ProjectID)
			if err != nil {
				return err
			}
			if !available && !allowRetained {
				return controlError(404, "Board connection unavailable.")
			}
			return decodeSQLiteRecord(st.ctx, tx, "board_credentials", c.ID, 1<<20, &protected)
		})
		if err != nil {
			return c, "", err
		}
		if in.Token == "" {
			plain, err := boardCredential(protected, false)
			if err != nil {
				return c, "", err
			}
			in.Token = string(plain)
		}
	}
	c.Mappings = cloneTransactionValueOfMappings(c.Mappings)
	if err := boardValidateToken(in.Token); err != nil {
		return c, "", err
	}
	return c, in.Token, nil
}
