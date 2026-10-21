package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (a *app) graphActivityReadyLocked(id string) bool {
	activity := a.state.graphActivity(id)
	if activity == nil || activity.Status != "ready" && activity.Status != "scheduled" {
		return false
	}
	for _, dependency := range activity.Dependencies {
		prerequisite := a.state.graphActivity(dependency)
		if prerequisite == nil || prerequisite.Status != "succeeded" {
			return false
		}
	}
	return true
}
func graphActivityBefore(left, right GraphActivity) bool {
	if (left.Correction != "") != (right.Correction != "") {
		return left.Correction != ""
	}
	if left.Priority != right.Priority {
		return left.Priority > right.Priority
	}
	return left.Order < right.Order
}
func (a *app) graphFirstReadyLocked(owner string) string {
	var first *GraphActivity
	for i := range a.state.GraphActivities {
		activity := &a.state.GraphActivities[i]
		if activity.ConversationID == owner && a.graphActivityReadyLocked(activity.ID) && (first == nil || graphActivityBefore(*activity, *first)) {
			first = activity
		}
	}
	if first == nil {
		return ""
	}
	return first.ID
}
func (a *app) scheduleGraphActivitiesLocked() {
	if a.closing || a.storageErr != nil {
		return
	}
	if a.graphStarting == nil {
		a.graphStarting = map[string]bool{}
	}
	ready := []GraphActivity{}
	for _, activity := range a.state.GraphActivities {
		if a.graphActivityReadyLocked(activity.ID) && a.state.activeGraphRun(activity.ConversationID) == nil && !a.graphStarting[activity.ConversationID] {
			ready = append(ready, activity)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool { return graphActivityBefore(ready[i], ready[j]) })
	chosen := map[string]bool{}
	for _, activity := range ready {
		if chosen[activity.ConversationID] {
			continue
		}
		chosen[activity.ConversationID] = true
		// Mark the capture reservation before releasing the scheduler mutex.
		a.graphStarting[activity.ConversationID] = true
		a.wg.Add(1)
		go func(id string) { defer a.wg.Done(); _ = a.startReservedGraphActivity(a.ctx, id) }(activity.ID)
	}
}

func (a *app) startGraphActivity(ctx context.Context, id string) error {
	a.mu.Lock()
	activity := a.state.graphActivity(id)
	if activity == nil {
		a.mu.Unlock()
		return errors.New("Activity not found.")
	}
	if a.graphStarting == nil {
		a.graphStarting = map[string]bool{}
	}
	if a.graphStarting[activity.ConversationID] {
		a.mu.Unlock()
		return errors.New("Conversation already has a graph preparing to start.")
	}
	a.graphStarting[activity.ConversationID] = true
	a.mu.Unlock()
	return a.startReservedGraphActivity(ctx, id)
}

func (a *app) startReservedGraphActivity(ctx context.Context, id string) error {
	a.mu.Lock()
	saved := a.state.graphActivity(id)
	if saved == nil {
		a.mu.Unlock()
		return errors.New("Activity not found.")
	}
	activity := *saved
	normalizeGraphWorkspaceDecision(&activity.Workspace)
	decisionErr := validateGraphAuthorization(&a.state, activity.ConversationID, activity.Authorization)
	if decisionErr == nil {
		decisionErr = validateGraphWorkspaceDecision(&a.state, activity.ConversationID, activity.Workspace, graphActivityHasRun(&a.state, activity.ID))
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.graphStarting, activity.ConversationID); a.mu.Unlock() }()
	fail := func(cause error) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		_ = a.graphChangeLocked(func(d *diskState) error {
			next := d.graphActivity(id)
			if next == nil || next.Version != activity.Version || next.Status == "running" {
				return nil
			}
			next.Status, next.Error, next.UpdatedAt = "awaiting_user", cause.Error(), now()
			next.Version++
			d.GraphNotifications = append(d.GraphNotifications, RunNotification{ID: "graph-activity:" + id + ":" + fmt.Sprint(next.Version), Kind: "activity_error", ActivityID: id, ConversationID: activity.ConversationID, Status: "pending", Error: cause.Error(), CreatedAt: now()})
			return nil
		})
		return cause
	}
	if decisionErr != nil {
		return fail(decisionErr)
	}
	snapshot, compiled, err := a.captureGraphSnapshot(ctx, activity.ProjectID, activity.GraphID)
	if err != nil {
		return fail(err)
	}
	for _, agent := range compiled.Agents {
		if err = CheckGraphAdapter(agent.Harness); err != nil {
			return fail(err)
		}
		a.mu.Lock()
		_, installed := a.binaries[agent.Harness]
		a.mu.Unlock()
		if !installed {
			return fail(errors.New("Graph harness is not installed: " + agent.Harness))
		}
	}
	for _, node := range compiled.Definition.Nodes {
		if node.Type == "terminal" {
			if _, err = powerShellExecutable(); err != nil {
				return fail(err)
			}
			if err = CheckGraphAdapter("pi"); err != nil {
				return fail(errors.New("Terminal owned-process containment is unavailable on this platform: " + err.Error()))
			}
		}
	}
	if err = validateGraphReuse(snapshot, activity.Workspace); err != nil {
		return fail(err)
	}
	if err = a.preflightGraphReuse(ctx, activity); err != nil {
		return fail(err)
	}
	a.mu.Lock()
	err = validateGraphWorkspaceDecision(&a.state, activity.ConversationID, activity.Workspace, graphActivityHasRun(&a.state, activity.ID))
	a.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	// Close the engine-owned CRUD gap between snapshot preparation and the actual
	// durable run reservation. External editors are still checked by revisions.
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	if err = a.revalidateGraphCapabilities(snapshot); err != nil {
		return fail(err)
	}
	a.mu.Lock()
	project := a.state.project(activity.ProjectID)
	if project == nil || project.Removed {
		a.mu.Unlock()
		return fail(errors.New("Graph project is unavailable."))
	}
	p := *project
	a.mu.Unlock()
	for path, source := range snapshot.Sources {
		current, readErr := readConfig(p, path)
		if readErr != nil || revision(current) != source.Revision {
			return fail(errors.New("Graph definitions changed before execution could be reserved; retry with current definitions."))
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.state.graphActivity(id)
	if current == nil || current.Version != activity.Version || !a.graphActivityReadyLocked(id) || a.graphFirstReadyLocked(activity.ConversationID) != id {
		return errors.New("Activity or scheduling priority changed while preparing its snapshot.")
	}
	if err = a.graphSlotAvailableLocked(activity.ConversationID); err != nil {
		return err
	}
	if err = validateGraphWorkspaceDecision(&a.state, activity.ConversationID, activity.Workspace, graphActivityHasRun(&a.state, activity.ID)); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	run := GraphRun{ID: newID(), ActivityID: id, ProjectID: activity.ProjectID, ConversationID: activity.ConversationID, GraphID: activity.GraphID, Input: GraphRunInput{Task: activity.Task}, Workspace: activity.Workspace, Snapshot: snapshot, Status: "starting", Revision: 1, CreatedAt: now(), UpdatedAt: now()}
	if run.Input.Task == "" {
		run.Input.Task = activity.Objective
	}
	err = a.graphChangeLocked(func(d *diskState) error {
		next := d.graphActivity(id)
		next.Workspace = activity.Workspace
		next.Status, next.UpdatedAt = "running", now()
		next.Version++
		d.GraphRuns = append(d.GraphRuns, run)
		s := d.session(run.ConversationID)
		s.SelectedGraphID, s.UpdatedAt = run.GraphID, now()
		return nil
	})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(a.ctx)
	g := &graphExecution{ctx: runCtx, cancel: cancel, done: make(chan struct{}), completed: make(chan graphCompletion), wake: make(chan struct{}, 1)}
	if a.graphRuns == nil {
		a.graphRuns = map[string]*graphExecution{}
	}
	a.graphRuns[run.ID] = g
	a.wg.Add(1)
	go a.runGraph(run.ID, g)
	return nil
}

func (d *diskState) graphNotification(id string) *RunNotification {
	for i := range d.GraphNotifications {
		if d.GraphNotifications[i].ID == id {
			return &d.GraphNotifications[i]
		}
	}
	return nil
}

// Called by the existing linked arbiter under app.mu. Its native turn reservation
// is shared with ordinary user turns and linked consultations.
func (a *app) scheduleGraphNotificationsLocked() {
	if a.closing || a.storageErr != nil {
		return
	}
	for _, notice := range a.state.GraphNotifications {
		if notice.Status != "pending" || a.runs[notice.ConversationID] != nil {
			continue
		}
		if notice.RetryAfter != "" {
			retry, _ := time.Parse(time.RFC3339Nano, notice.RetryAfter)
			if time.Now().Before(retry) {
				continue
			}
		}
		s := a.state.session(notice.ConversationID)
		if s == nil {
			continue
		}
		p := a.state.project(s.ProjectID)
		if p == nil || p.Removed || a.state.conversationArchived(s) {
			continue
		}
		b, ok := a.binaries[s.Harness]
		if !ok {
			continue
		}
		ctx, cancel := context.WithCancel(a.ctx)
		t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{}), graphNotificationID: notice.ID}
		native := a.state.Native[s.ID]
		session := *s
		cwd := p.Folder
		err := a.graphChangeLocked(func(d *diskState) error {
			notification := d.graphNotification(notice.ID)
			notification.Status, notification.TurnID = "delivering", newID()
			notification.Attempts++
			notification.RetryAfter = ""
			session := d.session(s.ID)
			session.Status, session.UpdatedAt = "running", now()
			session.Permissions, session.Questions = nil, nil
			return nil
		})
		if err != nil {
			cancel()
			continue
		}
		data := map[string]any{"notificationId": notice.ID, "activityId": notice.ActivityID, "error": notice.Error}
		if notice.ActivityID != "" {
			data["activity"] = a.state.graphActivity(notice.ActivityID)
		}
		if notice.RunID != "" {
			state, _ := a.graphInspectRunLocked(notice.ConversationID, notice.RunID)
			data["run"] = state
		}
		a.runs[s.ID] = t
		a.wg.Add(1)
		go func() {
			prompt, err := graphPrompt("run-result.md", data)
			if err == nil {
				// execute treats t.prompt as the complete submission. Pre-bound
				// notifications must seed it before the factory prepends policy.
				t.prompt = prompt
				var hooks *GraphAdapterHooks
				hooks, err = graphConversationFactory(a, t, session)
				if err == nil {
					err = BindGraphAdapter(t, *hooks)
				}
			}
			if err != nil {
				a.mu.Lock()
				delete(a.runs, session.ID)
				_ = a.graphChangeLocked(func(d *diskState) error { d.session(session.ID).Status = "error"; return nil })
				close(t.done)
				a.mu.Unlock()
				cancel()
				a.graphNotificationFinished(notice.ID, GraphAdapterResult{Outcome: "failed", Error: err})
				a.wg.Done()
				return
			}
			a.execute(t, session, native, b, cwd, submission{Text: prompt})
		}()
	}
}

func (a *app) graphNotificationFinished(id string, result GraphAdapterResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.graphChangeLocked(func(d *diskState) error {
		n := d.graphNotification(id)
		if n == nil || n.Status != "delivering" {
			return nil
		}
		// execute has released the native slot before Finished. The session may
		// already belong to another turn; this turn's result is authoritative.
		if result.Outcome == "completed" && result.Error == nil {
			n.Status, n.DeliveredAt, n.Error = "delivered", now(), ""
		} else {
			n.Status = "pending"
			n.RetryAfter = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano)
			if result.Error != nil {
				n.Error = result.Error.Error()
			}
		}
		return nil
	})
	a.scheduleLinkedLocked()
}
