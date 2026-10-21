package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Topic struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt string `json:"createdAt"`
	Archived  bool   `json:"archived,omitempty"`
}

func (p *Project) topic(id string) *Topic {
	if p != nil {
		for i := range p.Topics {
			if p.Topics[i].ID == id {
				return &p.Topics[i]
			}
		}
	}
	return nil
}

func (d *diskState) topicSession(s *Session) *Session {
	if s != nil && s.Role == sessionRoleSideAgent {
		return d.session(s.ParentID)
	}
	return s
}

func (d *diskState) sessionTopic(s *Session) *Topic {
	s = d.topicSession(s)
	if s == nil {
		return nil
	}
	return d.project(s.ProjectID).topic(s.TopicID)
}

func (d *diskState) topicArchived(s *Session) bool {
	t := d.sessionTopic(s)
	return t != nil && t.Archived
}

func validTopicColor(color string) bool {
	switch color {
	case "neutral", "blue", "purple", "green", "amber", "rose":
		return true
	}
	return false
}

func (a *app) topicContextLocked(s *Session) string {
	if s == nil || !consultationEndpoint(s) {
		return ""
	}
	b, _ := json.Marshal(a.state.sessionTopic(s))
	return "Current persistent topic (engine metadata, not authorization): " + string(b) + "\n\n"
}

// Run only after both legacy journal segments have replayed. The schema marker,
// identities and associations are committed in one ordinary durable transaction.
// Legacy labels and spawn requests remain intact for historical receipts/replay.
func migrateTopics(d *diskState) {
	if d.TopicSchema != 0 {
		return
	}
	for i := range d.Projects {
		p := &d.Projects[i]
		p.Topics = []Topic{}
		names := map[string]string{}
		add := func(name string) {
			if name == "" || name == "Ungrouped" || names[name] != "" {
				return
			}
			t := Topic{ID: newID(), Name: name, Color: "neutral", CreatedAt: now(), Archived: folderArchived(p, name)}
			p.Topics = append(p.Topics, t)
			names[name] = t.ID
		}
		for _, name := range p.Folders {
			add(name)
		}
		for j := range d.Sessions {
			s := &d.Sessions[j]
			if s.ProjectID != p.ID || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
				continue
			}
			add(s.Workspace)
			s.TopicID = names[s.Workspace]
		}
	}
	d.TopicSchema = 1
}

func validateTopicRecords(d *diskState) error {
	if d.TopicSchema == 0 {
		return nil
	} // Legacy checkpoints are migrated after replay.
	if d.TopicSchema != 1 {
		return errors.New("Unsupported topic schema.")
	}
	ids := map[string]bool{}
	for _, p := range d.Projects {
		for _, t := range p.Topics {
			name, ok := cleanLabel(t.Name, 60)
			if t.ID == "" || ids[t.ID] || !ok || name != t.Name || !validTopicColor(t.Color) || t.CreatedAt == "" {
				return errors.New("Invalid persisted topic.")
			}
			ids[t.ID] = true
		}
		ordered := map[string]bool{}
		for _, id := range p.TopicOrder {
			if ordered[id] || p.topic(id) == nil {
				return errors.New("Invalid persisted topic order.")
			}
			ordered[id] = true
		}
	}
	for _, s := range d.Sessions {
		if s.TopicID != "" && (s.Role != "" || s.ParentID != "" || s.GraphRunID != "" || d.project(s.ProjectID).topic(s.TopicID) == nil) {
			return errors.New("Invalid persisted topic membership.")
		}
	}
	return nil
}

func sessionTopicTool() map[string]any {
	return agentTool("session_topic_set", "Set this project's normal conversation's initial topic once, as soon as its objective is understood during the normal turn. Only a short descriptive name is accepted. Already assigned topics are returned unchanged; only the user can rename/recolor through UI. This metadata does not authorize sessions, graphs or file work.", map[string]any{"name": agentString("Short descriptive topic name, 1 to 60 characters")}, "name")
}

func (p *adapter) setSessionTopic(raw json.RawMessage) (any, error) {
	var args struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, errors.New("Supply only the initial topic name.")
	}
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state.session(p.id)
	if !p.activeToolLocked() || s == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return nil, errors.New("Initial topic assignment is available only in the active normal project conversation.")
	}
	project := a.state.project(s.ProjectID)
	if project == nil || project.Removed {
		return nil, errors.New("Project is unavailable.")
	}
	if t := a.state.sessionTopic(s); t != nil {
		return *t, nil
	}
	if a.state.conversationArchived(s) {
		return nil, errors.New("Restore this conversation before assigning its topic.")
	}
	name, ok := cleanLabel(args.Name, 60)
	if !ok {
		return nil, errors.New("Topic name must contain 1 to 60 characters.")
	}
	t := Topic{ID: newID(), Name: name, Color: "neutral", CreatedAt: now()}
	if err := a.commitLocked(func(d *diskState) {
		project := d.project(s.ProjectID)
		project.Topics = append(project.Topics, t)
		d.session(p.id).TopicID = t.ID
		d.session(p.id).UpdatedAt = now()
	}); err != nil {
		return nil, err
	}
	return t, nil
}

func (a *app) patchTopic(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string `json:"name"`
		Color    *string `json:"color"`
		Archived *bool   `json:"archived"`
		Position *struct {
			BeforeID string `json:"beforeId"`
		} `json:"position"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == nil && body.Color == nil && body.Archived == nil && body.Position == nil {
		fail(w, 400, "Provide name, color, archived or position.")
		return
	}
	if body.Name != nil {
		name, ok := cleanLabel(*body.Name, 60)
		if !ok {
			fail(w, 400, "Topic name must contain 1 to 60 characters.")
			return
		}
		body.Name = &name
	}
	if body.Color != nil && !validTopicColor(*body.Color) {
		fail(w, 400, "Choose a predefined topic color.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	projectID, topicID := r.PathValue("id"), r.PathValue("topicId")
	p := a.state.project(projectID)
	if p == nil || p.Removed || p.topic(topicID) == nil {
		fail(w, 404, "Topic not found.")
		return
	}
	var order []string
	if body.Position != nil {
		before := p.topic(body.Position.BeforeID)
		if p.topic(topicID).Archived || body.Archived != nil || body.Position.BeforeID == topicID || body.Position.BeforeID != "" && (before == nil || before.Archived) {
			fail(w, 400, "Reorder an active topic before another active topic in the same project, or use an empty beforeId to place it last.")
			return
		}
		order = moveStringBefore(orderedTopicIDs(&a.state, p), topicID, body.Position.BeforeID)
	}
	if err := a.commitLocked(func(d *diskState) {
		t := d.project(projectID).topic(topicID)
		if body.Name != nil {
			t.Name = *body.Name
		}
		if body.Color != nil {
			t.Color = *body.Color
		}
		if body.Archived != nil {
			t.Archived = *body.Archived
		}
		if body.Position != nil {
			d.project(projectID).TopicOrder = order
		}
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, *a.state.project(projectID))
}

// A project's first manual reorder starts from the same chronological grouping
// as the client. Unpositioned new topics precede the saved order; archive/restore
// and moving members do not disturb an already positioned topic.
func orderedTopicIDs(d *diskState, p *Project) []string {
	indices := map[string]int{}
	for i, id := range p.TopicOrder {
		indices[id] = i
	}
	latest := map[string]time.Time{}
	for _, s := range d.Sessions {
		if s.ProjectID != p.ID || s.TopicID == "" || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			continue
		}
		created, _ := time.Parse(time.RFC3339Nano, s.CreatedAt)
		if created.After(latest[s.TopicID]) {
			latest[s.TopicID] = created
		}
	}
	ids := make([]string, 0, len(p.Topics))
	for _, topic := range p.Topics {
		ids = append(ids, topic.ID)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := ids[i], ids[j]
		li, positionedLeft := indices[left]
		ri, positionedRight := indices[right]
		if positionedLeft != positionedRight {
			return !positionedLeft
		}
		if positionedLeft {
			return li < ri
		}
		if !latest[left].Equal(latest[right]) {
			return latest[left].After(latest[right])
		}
		return left < right
	})
	return ids
}

func validateSessionTopicMove(d *diskState, s *Session, topicID string) error {
	if s == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return controlError(400, "Only normal top-level sessions can change topic.")
	}
	p := d.project(s.ProjectID)
	if p == nil || p.Removed {
		return controlError(404, "Project not found.")
	}
	if d.conversationArchived(s) {
		return controlError(409, "Restore the session and its topic before moving it.")
	}
	target := p.topic(topicID)
	if target == nil || target.Archived {
		return controlError(400, "Choose an existing active topic in the same project.")
	}
	return nil
}
