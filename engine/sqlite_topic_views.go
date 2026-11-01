package main

import (
	"database/sql"
	"net/http"
)

type sqlTopicView struct {
	Topic
	Order    int64  `json:"order"`
	Revision string `json:"revision"`
}

func (a *app) getSQLTopic(w http.ResponseWriter, r *http.Request) {
	var topic Topic
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		topic, _, err = st.topic(r.PathValue("id"), r.PathValue("topicId"))
		return err
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, topic)
}

// Exact session lookup carries its compact grouping identity independently of
// whichever topic page a client currently has. No member histories are read.
func (st *sqliteSessionTx) sessionTopicView(s Session) (*sqlTopicView, error) {
	topic, err := st.sourceTopic(s)
	if err != nil || topic == nil {
		return nil, err
	}
	view := &sqlTopicView{Topic: *topic}
	if err := st.tx.QueryRowContext(st.ctx, `WITH ordered(id,position) AS (`+sqlTopicOrder+`) SELECT position FROM ordered WHERE id=?`, s.ProjectID, "", topic.ID).Scan(&view.Order); err != nil {
		return nil, err
	}
	if err := st.tx.QueryRowContext(st.ctx, `SELECT revision FROM data_projects WHERE id=?`, s.ProjectID).Scan(&view.Revision); err != nil {
		return nil, err
	}
	return view, nil
}

func (st *sqliteSessionTx) sessionActivityView(view *sqlSessionView) error {
	return st.tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_pending_permissions WHERE owner_id=?),(SELECT count(*) FROM data_pending_questions WHERE owner_id=?),coalesce((SELECT active FROM runtime_activity WHERE session_id=?),0),coalesce((SELECT reasoning_title FROM runtime_activity WHERE session_id=?),'')`, view.ID, view.ID, view.ID, view.ID).Scan(&view.PermissionCount, &view.QuestionCount, &view.RuntimeActive, &view.LatestReasoningTitle)
}
