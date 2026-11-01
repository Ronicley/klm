package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (a *app) scheduleSQLGraphNotificationsLocked() error {
	if a.closing || a.storageErr != nil || a.harnessInstalling {
		return nil
	}
	after := ""
	at := sqliteTimeKey(now())
	for {
		ids, err := sqliteQueryIDs(a.ctx, a.store.readers, `SELECT id FROM data_graph_notifications WHERE status='pending' AND (x_retryAfter='' OR x_retryAfter<=?) AND id>? ORDER BY id LIMIT 100`, at, after)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			after = id
			var notice RunNotification
			var session Session
			var native nativeSession
			folder := ""
			ready := false
			data := map[string]any{}
			err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
				if err := decodeSQLiteRecord(a.ctx, tx, "graph_notifications", id, 1<<20, &notice); err != nil {
					return err
				}
				if a.runs[notice.ConversationID] != nil {
					return nil
				}
				var err error
				session, err = st.spawnSource(notice.ConversationID)
				if err != nil {
					return err
				}
				project, err := st.project(session.ProjectID)
				if err != nil {
					return err
				}
				if project.Removed {
					return nil
				}
				archived, err := st.effectivelyArchived(session)
				if err != nil {
					return err
				}
				if archived {
					return nil
				}
				native, err = st.native(session.ID)
				if err != nil {
					return err
				}
				folder = project.Folder
				ready = true
				data = map[string]any{"notificationId": notice.ID, "activityId": notice.ActivityID, "error": notice.Error}
				if notice.ActivityID != "" {
					activity, err := st.graphActivity(session.ID, notice.ActivityID)
					if err != nil {
						return err
					}
					data["activity"], err = sqlGraphWire(activity)
					if err != nil {
						return err
					}
				}
				if notice.RunID != "" {
					run, err := st.graphRun(session.ID, notice.RunID)
					if err != nil {
						return err
					}
					data["run"] = map[string]any{"runId": run.ID, "activityId": run.ActivityID, "graphId": run.GraphID, "status": run.Status, "active": graphRunActive(run.Status), "task": run.Input.Task, "result": run.Result, "finalityError": run.FinalityError}
				}
				return nil
			})
			if err != nil {
				return err
			}
			if !ready {
				continue
			}
			b, installed := a.binaries[session.Harness]
			if !installed {
				continue
			}
			cwd, err := existingDirectory(folder)
			if err != nil {
				continue
			}
			prompt, err := graphPrompt("run-result.md", data)
			if err != nil {
				cause := err
				err = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
					notice.Error = cause.Error()
					notice.RetryAfter = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano)
					return st.putRoot("graph_notifications", id, notice)
				})
				if err != nil {
					return err
				}
				continue
			}
			err = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
				if notice.Attempts == ^uint64(0) {
					return errors.New("Graph notification attempt counter exhausted.")
				}
				notice.Status, notice.TurnID, notice.RetryAfter = "delivering", newID(), ""
				notice.Attempts++
				if err := st.putRoot("graph_notifications", id, notice); err != nil {
					return err
				}
				meta, err := st.sessionMeta(session.ID)
				if err != nil {
					return err
				}
				meta.Status, meta.UpdatedAt = "running", now()
				if err := st.putSessionMeta(meta); err != nil {
					return err
				}
				for _, table := range []string{"pending_permissions", "pending_questions"} {
					if err := st.clearChildren(table, session.ID); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(a.ctx)
			t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{}), graphNotificationID: id}
			a.runs[session.ID] = t
			a.wg.Add(1)
			go a.execute(t, session, native, b, cwd, submission{Text: prompt})
		}
	}
}
