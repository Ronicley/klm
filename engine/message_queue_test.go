package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func steeringFixture(t *testing.T) (*app, *adapter) {
	t.Helper()
	a := generalControlFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	active := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.runs["target"] = active
	if err := a.commitLocked(func(d *diskState) {
		d.QueuePayloads = map[string]queuedPayload{}
		for _, id := range []string{"a", "b", "c"} {
			d.session("target").Queue = append(d.session("target").Queue, QueuedMessage{ID: id, Text: "instruction " + id, Mode: "steer", Status: "steering"})
			d.QueuePayloads[id] = queuedPayload{Harness: "pi", Directory: d.project("p2").Folder, Submission: submission{Text: "instruction " + id, Context: "attachment " + id}}
		}
	}); err != nil {
		t.Fatal(err)
	}
	return a, &adapter{app: a, id: "target", harness: "pi", turn: active}
}

func TestSteeringBatchConsumptionAndRecovery(t *testing.T) {
	for _, consume := range []bool{true, false} {
		name := "interrupted"
		if consume {
			name = "consumed"
		}
		t.Run(name, func(t *testing.T) {
			a, p := steeringFixture(t)
			q, payload, err := p.takeSteering()
			if err != nil || q == nil || len(p.turn.steeringBatches[q.ID]) != 3 {
				t.Fatalf("batch claim: %v, %v", q, err)
			}
			for _, id := range []string{"a", "b", "c"} {
				if !strings.Contains(payload.piText(), "instruction "+id) || !strings.Contains(payload.piText(), "attachment "+id) {
					t.Fatal("batch lost an instruction or its prepared context")
				}
			}
			if err := p.acknowledgeSteering(q.ID); err != nil {
				t.Fatal(err)
			}
			if len(a.state.session("target").Events) != 0 || len(a.state.QueuePayloads) != 3 {
				t.Fatal("in-memory native ACK prematurely consumed durable inputs")
			}
			for _, pending := range a.state.session("target").Queue {
				if pending.Status != "accepted" {
					t.Fatal("native pending status missing")
				}
			}
			if consume {
				if err := p.finishSteering(q.ID, ""); err != nil {
					t.Fatal(err)
				}
				if len(a.state.session("target").Queue) != 0 || len(a.state.QueuePayloads) != 0 || len(a.state.session("target").Events) != 3 {
					t.Fatal("native consumption must settle every input once")
				}
			} else {
				p.turn.cancel()
				if err := p.finishTurnStateLocked(context.Canceled); err != nil {
					t.Fatal(err)
				}
				for _, pending := range a.state.session("target").Queue {
					if pending.Status != "uncertain" {
						t.Fatal("interruption lost recovery for a native-pending input")
					}
				}
			}
		})
	}
}

func TestSteeringNativeCorrelation(t *testing.T) {
	t.Run("pi", func(t *testing.T) {
		a, p := steeringFixture(t)
		_, payload, err := p.takeSteering()
		if err != nil {
			t.Fatal(err)
		}
		if !p.hasUnconfirmedSteering() {
			t.Fatal("runtime must not be reusable")
		}
		if err := p.finishPiSteering("unrelated user text"); err != nil {
			t.Fatal(err)
		}
		if len(a.state.QueuePayloads) != 3 {
			t.Fatal("unrelated message consumed input")
		}
		if err := p.finishPiSteering(payload.piText()); err != nil {
			t.Fatal(err)
		}
		if p.hasUnconfirmedSteering() || len(a.state.QueuePayloads) != 0 {
			t.Fatal("correlated batch was not consumed")
		}
	})
	t.Run("codex", func(t *testing.T) {
		a, p := steeringFixture(t)
		p.harness = "codex"
		q, _, err := p.takeSteering()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.acknowledgeSteering(q.ID); err != nil {
			t.Fatal(err)
		}
		c := &codexInteractive{p: p, threadID: "thread1", turnID: "turn1", items: map[string]map[string]any{}}
		frame := map[string]any{"method": "item/started", "params": map[string]any{"threadId": "other", "turnId": "turn1", "item": map[string]any{"type": "userMessage", "id": "item1", "clientId": q.ID}}}
		if err := c.frame(frame); err != nil {
			t.Fatal(err)
		}
		if len(a.state.QueuePayloads) != 3 {
			t.Fatal("foreign thread consumed input")
		}
		object(frame["params"])["threadId"] = "thread1"
		if err := c.frame(frame); err != nil {
			t.Fatal(err)
		}
		if len(a.state.QueuePayloads) != 0 {
			t.Fatal("native user incorporation did not consume batch")
		}
	})
	t.Run("opencode-child", func(t *testing.T) {
		a, p := steeringFixture(t)
		p.harness = "opencode"
		a.state.Native[p.id] = nativeSession{ID: "parent"}
		if _, err := p.steeringBoundary("klm/steering/take", []byte(`{"sessionID":"child"}`)); err != nil {
			t.Fatal(err)
		}
		if p.hasUnconfirmedSteering() {
			t.Fatal("child claimed parent input")
		}
		if _, err := p.steeringBoundary("klm/steering/take", []byte(`{"sessionID":"parent"}`)); err != nil {
			t.Fatal(err)
		}
		if !p.hasUnconfirmedSteering() || len(a.state.QueuePayloads) != 3 {
			t.Fatal("boundary must retain every payload until complete-part confirmation")
		}
	})
}

func TestSendNowContinuationRequiresConfirmedStop(t *testing.T) {
	for _, scenario := range []string{"replace", "explicit-stop", "shutdown-failure", "engine-closing"} {
		t.Run(scenario, func(t *testing.T) {
			a, p := steeringFixture(t)
			id, err := a.changeQueuedMessageLocked("target", "b", false, false, nil)
			if err != nil || id != "b" || p.turn.ctx.Err() == nil || p.turn.restartMessageID != "b" {
				t.Fatalf("Send now did not reserve replacement and cancel the turn: %s, %v", id, err)
			}
			queue := a.state.session("target").Queue
			if queue[0].ID != "b" || queue[0].Status != "interrupting" || queue[1].Status != "paused" || queue[2].Status != "paused" {
				t.Fatal("replacement must be exclusive and preserve other inputs")
			}
			switch scenario {
			case "explicit-stop":
				if _, err := a.stopSessionLocked("target", nil); err != nil {
					t.Fatal(err)
				}
			case "shutdown-failure":
				p.turn.stopErr = errors.New("shutdown unconfirmed")
			case "engine-closing":
				a.closing = true
			}
			if err := p.finishTurnStateLocked(context.Canceled); err != nil {
				t.Fatal(err)
			}
			want := "paused"
			if scenario == "replace" {
				want = "queued"
			}
			queue = a.state.session("target").Queue
			if queue[0].Status != want || len(a.state.QueuePayloads) != 3 {
				t.Fatalf("replacement status = %s, want %s; payloads must survive", queue[0].Status, want)
			}
		})
	}
}
