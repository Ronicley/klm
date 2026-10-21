package main

import "testing"

func TestHappeningReasoningFollowsCurrentTurn(t *testing.T) {
	start := 1
	a := &app{
		state: diskState{Sessions: []Session{{ID: "session", Status: "running", Events: []Event{
			{Type: "reasoning", Text: "Previous turn"},
		}}}},
		runs: map[string]*turn{"session": {}},
	}
	check := func(want string) {
		t.Helper()
		if got := a.sessionSummaryLocked("session").LatestReasoningTitle; got != want {
			t.Fatalf("reasoning subtitle = %q, want %q", got, want)
		}
	}
	check("") // Reserved turn has not begun execution yet.
	a.runs["session"].reasoningStart = &start
	check("") // Working again, but no new reasoning yet.
	s := a.state.session("session")
	s.Events = append(s.Events, Event{Type: "reasoning", Text: "**New turn**\nMore reasoning"})
	check("New turn")
	s.Events = append(s.Events, Event{Type: "reasoning", Text: ""})
	check("New turn") // Empty chunk does not erase current-turn reasoning.
	s.Status = "idle"
	check("") // Clears even before the old turn reservation is removed.
	delete(a.runs, "session")
	check("")
	s.Status = "running"
	start = len(s.Events)
	a.runs["session"] = &turn{reasoningStart: &start}
	check("") // Previous reasoning cannot return on the next turn.
	s.Events = append(s.Events, Event{Type: "reasoning", Title: "Fresh title", Text: "Fresh body"})
	check("Fresh title")
}
