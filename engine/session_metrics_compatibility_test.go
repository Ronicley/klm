package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacySessionMetricsRejectSQLite(t *testing.T) {
	a := &app{store: &sqliteStore{}}
	for _, handler := range []http.HandlerFunc{a.sessionMetrics, a.sessionExecutions} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/api/metrics/sessions", nil))
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("SQLite metrics must not report empty legacy totals: HTTP %d", response.Code)
		}
	}
}
