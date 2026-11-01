package main

import "testing"

func TestMergeRouteRegistration(t *testing.T) {
	for _, store := range []*sqliteStore{nil, {}} {
		a := &app{store: store}
		if a.routes() == nil {
			t.Fatal("missing HTTP handler")
		}
	}
}
