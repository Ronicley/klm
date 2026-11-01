package main

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const sessionRoleGeneralAgent = "general_agent"

// Recovery notices can precede the first human input. Legacy history remains
// conservative, but these attributed internal notices are not harness selection.
func (d *diskState) generalHarnessInputAccepted(s *Session) bool {
	if len(s.Queue) > 0 {
		return true
	}
	for key := range d.AcceptedMessages {
		if strings.HasPrefix(key, s.ID+"/") {
			return true
		}
	}
	for _, e := range s.Events {
		notice := e.Type == "status" && e.Data != nil && e.Data["automationRecoveryNotice"] == true
		if e.Type == "status" && !notice {
			for _, r := range d.AutomationRecoveries {
				if r.NoticeEventID == e.ID {
					notice = true
					break
				}
			}
		}
		if !notice {
			return true
		}
	}
	return false
}

func (d *diskState) normalizeGeneralHarnessLock(s *Session) {
	if d.generalHarnessInputAccepted(s) {
		s.HarnessLocked = true
		return
	}
	// Repair the earlier events-count migration only with positive evidence of
	// recovery-only history. An opaque legacy lock with no history stays locked.
	if len(s.Events) > 0 {
		s.HarnessLocked = false
	}
}

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
	topicPrompt := ""
	if s.Role == "" && s.ParentID == "" && s.GraphRunID == "" {
		if s.TopicID == "" {
			topicPrompt = "session-topic-pending.md"
		} else {
			topicPrompt = "session-topic-assigned.md"
		}
	} else if s.Role == sessionRoleSideAgent {
		topicPrompt = "session-topic-side.md"
	}
	if topicPrompt != "" {
		body, err := loadInternalPrompt(topicPrompt)
		if err != nil {
			return "", err
		}
		guidance += "\n\n" + strings.ReplaceAll(body, "{{topicId}}", s.TopicID)
	}
	if s.Automation != nil && s.Automation.Status == "running" && s.Role == "" && s.ParentID == "" {
		body, err := loadInternalPrompt("automation-run.md")
		if err != nil {
			return "", err
		}
		guidance += "\n\n" + body
	}
	return guidance, nil
}

func (a *app) generalConversation(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	id, err := a.ensureGeneralSession()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	if a.store != nil {
		a.respondSQLSession(w, r, id, 200)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	respond(w, 200, a.currentSessionUpdateLocked(id))
}

func (a *app) ensureGeneralSession() (string, error) {
	// Directory creation is idempotent and happens outside the state mutex.
	cwd, err := filepath.Abs(filepath.Join(a.dir, "workspaces", "general-agent"))
	if err == nil {
		err = os.MkdirAll(cwd, 0700)
	}
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store != nil {
		var id string
		err := a.store.readers.QueryRowContext(a.ctx, `SELECT id FROM data_sessions WHERE role='general_agent' LIMIT 1`).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
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
		if err := a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error { return st.createSession(s, a.dir) }); err != nil {
			return "", err
		}
		return s.ID, nil
	}
	if s := a.state.generalSession(); s != nil {
		return s.ID, nil
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
		return "", err
	}
	return s.ID, nil
}
