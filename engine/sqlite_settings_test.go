package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLiteTranscriptionSettingsAndFavorite(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	req := httptest.NewRequest("PATCH", "http://localhost/api/transcription/settings", strings.NewReader(`{"apiKey":"synthetic-transcription-key","language":"en","vocabulary":[{"term":"KLM","note":"product"}]}`))
	res := httptest.NewRecorder()
	a.updateTranscriptionSettings(res, req)
	if res.Code != 200 || strings.Contains(res.Body.String(), "synthetic-transcription-key") {
		t.Fatal("settings write or secret projection", res.Code, res.Body.String())
	}
	settings, err := a.readSQLTranscription(a.ctx)
	if err != nil || settings.APIKey != "synthetic-transcription-key" || len(settings.Vocabulary) != 1 {
		t.Fatal("SQL transcription settings", err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(s.ID, Event{ID: "response", Type: "assistant", Text: "Completed", Status: "completed"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("PATCH", "http://localhost/favorite", bytes.NewBufferString(`{"favorite":true}`))
	req.SetPathValue("id", s.ID)
	req.SetPathValue("eventID", "response")
	res = httptest.NewRecorder()
	a.patchEvent(res, req)
	var event Event
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &event) != nil || !event.Favorite {
		t.Fatal("favorite mutation", res.Code, res.Body.String())
	}
	if a.storageErr != nil || len(a.state.Sessions) != 0 {
		t.Fatal("legacy fallback", a.storageErr)
	}
}
