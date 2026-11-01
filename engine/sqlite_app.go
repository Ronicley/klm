package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

func (s *sqliteStore) readSnapshot(ctx context.Context, read func(*sql.Tx) error) error {
	tx, err := s.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := read(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) readProjectMeta(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.readSnapshot(ctx, func(tx *sql.Tx) error { return decodeSQLiteRecord(ctx, tx, "projects", id, 1<<20, &p) })
	return p, err
}

// app.mu owns active handles; the SQL gate owns durable records. Effects and
// client publication happen after COMMIT, never from an optimistic state copy.
func (a *app) writeSQLLocked(ctx context.Context, change func(*sqliteSessionTx) error) error {
	if ctx == nil {
		ctx = a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
	}
	if a.closing || a.storageErr != nil {
		return controlError(503, "Engine is unavailable.")
	}
	return a.persistSQLLocked(ctx, change)
}

// Owned finalizers may drain already-produced output after admission closes.
// Main shutdown waits for these writers before closing/checkpointing the DB.
func (a *app) persistSQLLocked(ctx context.Context, change func(*sqliteSessionTx) error) error {
	if a.storageErr != nil {
		return a.storageErr
	}
	changes, err := a.store.sessionTransaction(ctx, change)
	if err != nil {
		var sqliteFailure interface{ Code() int }
		if errors.As(err, &sqliteFailure) {
			return a.failStorageLocked(err)
		}
		return err
	}
	if a.sqlChanges != nil {
		a.sqlChanges.publish(changes)
	}
	return nil
}

func sqlHTTPError(w http.ResponseWriter, err error) {
	var request *sqliteRequestError
	var control *sessionControlError
	var agent *AgentConfigProblem
	switch {
	case errors.Is(err, errSQLiteContentChanged):
		fail(w, 409, err.Error())
	case errors.As(err, &agent):
		respond(w, agent.status, agent)
	case errors.Is(err, sql.ErrNoRows):
		fail(w, 404, "Record not found.")
	case errors.As(err, &request):
		fail(w, request.Status, request.Message)
	case errors.As(err, &control):
		fail(w, control.code, control.text)
	case errors.Is(err, context.Canceled):
		fail(w, 503, "Request interrupted.")
	default:
		fail(w, 503, err.Error())
	}
}

func (st *sqliteSessionTx) eachMatching(table, where string, args []any, visit func(string) error) error {
	if !sqliteKnownTable(table) {
		return errors.New("unknown SQL domain")
	}
	after := ""
	for {
		values := append(append([]any{}, args...), after)
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_`+table+` WHERE (`+where+`) AND id>? ORDER BY id LIMIT 100`, values...)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := visit(id); err != nil {
				return err
			}
			after = id
		}
	}
}
