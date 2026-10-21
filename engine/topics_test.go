package main

import "testing"

func TestArchivedTopicDefersContinuationTurns(t *testing.T) {
	for _, kind := range []string{"consultation answer", "graph result"} {
		t.Run(kind, func(t *testing.T) {
			a := &app{
				dir: t.TempDir(),
				state: diskState{
					Version: 2, TopicSchema: 1,
					Projects: []Project{{ID: "p", Folder: t.TempDir(), Folders: []string{}, Topics: []Topic{{ID: "topic", Name: "Work", Color: "neutral", CreatedAt: now(), Archived: true}}}},
					Sessions: []Session{
						{ID: "main", ProjectID: "p", TopicID: "topic", Harness: "pi", Status: "idle", Events: []Event{}},
						{ID: "other", ProjectID: "p", TopicID: "topic", Harness: "pi", Status: "idle", Events: []Event{}},
					},
					Native: map[string]nativeSession{},
				},
				binaries: map[string]binary{"pi": {}},
				runs:     map[string]*turn{},
			}
			// There is intentionally no execution context or runnable harness.
			// Archive deferral must happen before reserving any native turn.
			if kind == "consultation answer" {
				a.state.Consultations = []Consultation{{ID: "request", From: "main", To: "other", Question: "Question", Answer: "Answer", Status: "completed", Delivery: "pending", CreatedAt: now()}}
				a.scheduleLinkedLocked()
				if a.state.Consultations[0].Delivery != "pending" {
					t.Fatal("archived consultation answer must remain pending for restoration")
				}
			} else {
				a.state.GraphNotifications = []RunNotification{{ID: "notice", ConversationID: "main", Status: "pending"}}
				a.scheduleGraphNotificationsLocked()
				if notice := a.state.GraphNotifications[0]; notice.Status != "pending" || notice.Attempts != 0 {
					t.Fatal("archived graph result must remain pending without a delivery attempt")
				}
			}
			if len(a.runs) != 0 || a.state.session("main").Status != "idle" || a.state.GraphRevision != 0 {
				t.Fatal("archived topic continuation reserved a turn or changed durable state")
			}
		})
	}
}
