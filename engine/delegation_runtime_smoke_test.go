package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// One local storage/control smoke, without a provider or native process.
func TestDelegationRuntimeRequestsRecovery(t *testing.T) {
	store, ctx, project, parent := delegationStorageFixture(t)
	child, owned, batch, item := delegationStorageRecords(parent, project.Folder, "runtime")
	batch.NativeCall.Delivery = DelegationDeliveryPending
	item.State, item.DispatchAttempted = DelegationInvocationPreparing, true
	dir := t.TempDir()
	_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		return persistDelegationStorageFixture(st, dir, child, owned, batch, item)
	})
	if err != nil {
		t.Fatal(err)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rootTurn := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{})}
	a := &app{ctx: ctx, store: store, runs: map[string]*turn{parent.ID: rootTurn}}
	root := &adapter{app: a, id: parent.ID, turn: rootTurn, harness: parent.Harness}
	owner := &delegationExecution{parent: root}
	childTurn := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{}), delegationChild: &delegationChildBinding{owner: owner, invocationID: item.ID}}
	a.runs[child.ID] = childTurn
	p := &adapter{app: a, id: child.ID, turn: childTurn, harness: child.Harness, role: sessionRoleSubagent, cwd: project.Folder}
	allowed := false
	if err := p.requestPermission(Permission{Kind: "fixture_guard", SourceID: "fixture", Title: "Fixture permission"}, nil, func(value bool) error { allowed = value; return nil }); err != nil {
		t.Fatal(err)
	}
	requests, more, err := a.readDelegationRequests(ctx, parent.ID, "")
	if err != nil || more || len(requests) != 1 {
		t.Fatalf("owner request projection: %+v %t %v", requests, more, err)
	}
	var permission Permission
	if err := json.Unmarshal(requests[0].Request, &permission); err != nil {
		t.Fatal(err)
	}
	if err := a.replySQLPermission(child.ID, permission.ID, "once", nil, nil); err == nil {
		t.Fatal("child public reply was writable")
	}
	if err := a.replySQLPermission(parent.ID, permission.ID, "once", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("exact owner reply did not reach child")
	}
	if err := store.runDelegationRecoveryBarrier(ctx, recoverDelegationRuntime); err != nil {
		t.Fatal(err)
	}
	err = store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		entry, err := st.delegationChild(child.ID)
		if err != nil {
			return err
		}
		if !entry.Quarantined || entry.Reusable() {
			t.Fatal("restart invented dispatched child drainage")
		}
		settled, err := st.delegationBatch(batch.ID)
		if err != nil {
			return err
		}
		if settled.State != DelegationSettled || settled.Outcome != DelegationFinalityUnconfirmed || settled.Delivery != DelegationDeliveryCancelled {
			t.Fatalf("restart result %+v", settled)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.delegationRoutes(mux)
	for _, suffix := range []string{"batches", "batches/" + batch.ID + "/members", "children", "requests"} {
		r := httptest.NewRequest(http.MethodGet, "/api/sessions/"+parent.ID+"/delegation/"+suffix, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
}
