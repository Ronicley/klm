package main

import (
	"errors"
	"net/http"
	"path/filepath"
)

func projectChatAgent(project Project, id string) (AgentRecord, error) {
	catalog, err := loadAuthoring(project)
	if err != nil {
		return AgentRecord{}, err
	}
	for _, agent := range catalog.Agents {
		if agent.ID == id && agent.Enabled {
			return agent, nil
		}
	}
	return AgentRecord{}, errors.New("Selected agent is unavailable. Reload agents and select another agent or None.")
}

func (s Session) selectedAgentID() string {
	if s.SelectedAgent == nil {
		return ""
	}
	return s.SelectedAgent.ID
}

func (a *app) selectConversationAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID *string `json:"selectedAgentId"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AgentID == nil || *body.AgentID != "" && !validAuthoringID(*body.AgentID) {
		fail(w, 400, "Provide a valid selectedAgentId or an empty string for None.")
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	id := r.PathValue("id")
	a.mu.Lock()
	s := a.state.session(id)
	if s == nil || s.Role != "" || s.ParentID != "" || a.state.project(s.ProjectID) == nil || a.state.project(s.ProjectID).Removed {
		a.mu.Unlock()
		fail(w, 404, "Project conversation not found.")
		return
	}
	if a.runs[id] != nil || len(s.Queue) != 0 {
		a.mu.Unlock()
		fail(w, 409, "Finish the current turn and queued messages before changing agents.")
		return
	}
	before, project := *s, *a.state.project(s.ProjectID)
	a.mu.Unlock()
	var agent AgentRecord
	var err error
	if *body.AgentID != "" {
		agent, err = projectChatAgent(project, *body.AgentID)
		if err == nil {
			err = a.validateAgentModel(r, project, agent.DefaultHarness, agent.Model, agent.Effort)
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	a.mu.Lock()
	s = a.state.session(id)
	currentProject := a.state.project(project.ID)
	if a.closing || a.storageErr != nil || r.Context().Err() != nil || s == nil || currentProject == nil || currentProject.Removed || s.ProjectID != project.ID || a.runs[id] != nil || len(s.Queue) != 0 || s.Harness != before.Harness || s.Model != before.Model || s.Effort != before.Effort || s.selectedAgentID() != before.selectedAgentID() || currentProject.Folder != project.Folder {
		a.mu.Unlock()
		fail(w, 409, "Conversation changed. Retry selecting the agent.")
		return
	}
	changedHarness := *body.AgentID != "" && s.Harness != agent.DefaultHarness
	if *body.AgentID != "" {
		if _, installed := a.binaries[agent.DefaultHarness]; !installed {
			a.mu.Unlock()
			fail(w, 400, "Agent harness is not installed.")
			return
		}
	}
	err = a.commitLocked(func(d *diskState) {
		s := d.session(id)
		s.SelectedAgent, s.AgentSelectionLocked, s.UpdatedAt = nil, false, now()
		if *body.AgentID != "" {
			s.SelectedAgent = &agent
			s.Harness, s.Model, s.Effort = agent.DefaultHarness, agent.Model, agent.Effort
			s.ResolvedModel, s.ResolvedEffort, s.Usage = "", "", nil
		}
		if changedHarness {
			delete(d.Native, id)
			if s.Harness == "pi" {
				d.Native[id] = nativeSession{Path: filepath.Join(a.dir, "sessions", id+"-"+newID()+".jsonl")}
			}
		}
	})
	if err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	runtime := a.runtimes[id]
	if changedHarness {
		delete(a.runtimes, id)
	}
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	if changedHarness && runtime != nil {
		_ = runtime.shutdown()
	}
	respond(w, 200, response)
}

func (a *app) chatAgentPrompt(s Session) (string, error) {
	if s.SelectedAgent == nil {
		return "", nil
	}
	a.mu.Lock()
	project := a.state.project(s.ProjectID)
	if project == nil || project.Removed {
		a.mu.Unlock()
		return "", errors.New("Agent project is unavailable.")
	}
	p := *project
	a.mu.Unlock()
	a.authoringMu.Lock()
	agent, err := projectChatAgent(p, s.selectedAgentID())
	a.authoringMu.Unlock()
	if err != nil {
		return "", err
	}
	if agent.DefaultHarness != s.Harness {
		return "", errors.New("Agent harness changed. Select the agent again before sending another message.")
	}
	return agent.Prompt, nil
}
