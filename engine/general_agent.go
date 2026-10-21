package main

import (
	"net/http"
	"os"
	"path/filepath"
)

const sessionRoleGeneralAgent = "general_agent"

// A general conversation is engine-owned, never a synthetic user project.
func (d *diskState) generalSession() *Session {
	for i := range d.Sessions {
		if d.Sessions[i].Role == sessionRoleGeneralAgent {
			return &d.Sessions[i]
		}
	}
	return nil
}

// Shared by message preparation, queue execution and catalog discovery. Ordinary
// chats retain their registered project directory; engine-owned chats use theirs.
func (d *diskState) conversationDirectory(s *Session) (string, bool) {
	if s == nil {
		return "", false
	}
	if s.Role == sessionRoleGeneralAgent {
		return s.ExecutionCWD, s.ExecutionCWD != ""
	}
	if s.Role == sessionRoleSubagent && s.ProjectID == "" {
		return d.conversationDirectory(d.session(s.ParentID))
	}
	p := d.project(s.ProjectID)
	if p == nil || p.Removed {
		return "", false
	}
	return p.Folder, true
}

func conversationGuidance(s *Session) (string, error) {
	if s.Role == sessionRoleGeneralAgent {
		return loadInternalPrompt("general-agent.md")
	}
	guidance, err := sessionCollaborationPrompt()
	if err != nil {
		return "", err
	}
	if s.Role == "" && s.ParentID == "" && s.GraphRunID == "" {
		if s.TopicID == "" {
			guidance += "\n\nThis conversation has no topic yet. As soon as you understand its objective, call session_topic_set with a short descriptive name during this normal turn, including clarification turns. Do so before spawning sessions. Do not start an extra classification turn. This is initial metadata only, not additional authorization."
		} else {
			guidance += "\n\nThis conversation already has a persistent topic (ID: " + s.TopicID + "). Keep its association. Only the user can rename or recolor it through UI; repeated session_topic_set calls return the current topic unchanged."
		}
	} else if s.Role == sessionRoleSideAgent {
		guidance += "\n\nThis side chat uses its main conversation's topic. You cannot assign or change it. Spawn inherits the main topic and requires it to be assigned first."
	}
	return guidance, nil
}

func (a *app) generalConversation(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	// Directory creation is idempotent and happens outside the state mutex.
	cwd, err := filepath.Abs(filepath.Join(a.dir, "workspaces", "general-agent"))
	if err == nil {
		err = os.MkdirAll(cwd, 0700)
	}
	if err != nil {
		fail(w, 503, "Cannot prepare the general agent workspace.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.state.generalSession(); s != nil {
		respond(w, 200, a.currentSessionUpdateLocked(s.ID))
		return
	}
	harness := "pi"
	for _, option := range a.harnesses {
		if option.Available {
			harness = option.ID
			break
		}
	}
	s := newTopLevelSession("", "General agent", "", harness, "", "")
	s.Role, s.ExecutionCWD = sessionRoleGeneralAgent, cwd
	if err := a.commitLocked(func(d *diskState) { a.appendTopLevelSession(d, s) }); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 201, a.currentSessionUpdateLocked(s.ID))
}
