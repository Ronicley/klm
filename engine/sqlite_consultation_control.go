package main

import (
	"database/sql"
	"net/http"
)

func (a *app) cancelSQLConsultation(w http.ResponseWriter, r *http.Request) {
	var c Consultation
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		if err := decodeSQLiteRecord(st.ctx, st.tx, "consultations", r.PathValue("requestID"), 1<<20, &c); err != nil {
			return err
		}
		if c.From != r.PathValue("id") && c.To != r.PathValue("id") {
			return sql.ErrNoRows
		}
		if !consultationTerminal(c.Status) {
			c.Status, c.Error = "cancelled", "Consultation cancelled."
		}
		c.Delivery, c.UpdatedAt = "cancelled", now()
		if err := st.putRoot("consultations", c.ID, c); err != nil {
			return err
		}
		return st.syncConsultationCards(c, c.UpdatedAt)
	})
	if err == nil {
		if t := a.runs[c.To]; t != nil && t.consultationID == c.ID {
			t.cancel()
		}
		if t := a.runs[c.From]; t != nil && t.consultationID == c.ID && t.delivery {
			t.cancel()
		}
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.respondSQLSession(w, r, r.PathValue("id"), 200)
}
