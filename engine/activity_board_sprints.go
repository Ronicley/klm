package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Choices combine confirmed task paths and the default team's provider-reported
// current sprint. They never author or change a remote iteration.
type activityBoardSprint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Organization string `json:"organization"`
	Project      string `json:"project"`
	Current      bool   `json:"current,omitempty"`
}

type boardSprintSource struct {
	connection activityBoardConnection
	credential []byte
}

func boardSprintFromPath(c activityBoardConnection, path string) activityBoardSprint {
	name := path
	if index := strings.LastIndex(name, "\\"); index >= 0 {
		name = name[index+1:]
	}
	if strings.EqualFold(path, c.Scope.Project) {
		name = "No sprint"
	}
	return activityBoardSprint{ID: boardSprintKey(c, activityBoardItem{IterationPath: path}), Name: name, Path: path, Organization: c.Scope.Organization, Project: c.Scope.Project}
}

func boardAzureCurrentSprints(ctx context.Context, c activityBoardConnection, token string) ([]activityBoardSprint, error) {
	type iteration struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		Attributes struct {
			TimeFrame string `json:"timeFrame"`
		} `json:"attributes"`
	}
	var response struct {
		Value  []iteration `json:"value"`
		Values []iteration `json:"values"` // Also used in the documented Azure example.
	}
	// Omitting team uses Azure's project default team, explicitly chosen by the user.
	path := boardAzureProjectPath(c) + "/_apis/work/teamsettings/iterations?$timeframe=current&api-version=7.1"
	if err := boardAPI(ctx, c, token, "GET", path, nil, &response); err != nil {
		return nil, err
	}
	rows := response.Value
	if rows == nil {
		rows = response.Values
	}
	if rows == nil {
		return nil, errors.New("Azure returned an invalid current sprint response.")
	}
	result := make([]activityBoardSprint, 0, len(rows))
	for _, row := range rows {
		if row.Path == "" || row.Attributes.TimeFrame != "" && !strings.EqualFold(row.Attributes.TimeFrame, "current") {
			return nil, errors.New("Azure did not confirm a current sprint path. Refresh tasks and try again.")
		}
		if !strings.EqualFold(row.Path, c.Scope.Project) && !strings.HasPrefix(strings.ToLower(row.Path), strings.ToLower(c.Scope.Project)+"\\") {
			return nil, errors.New("Azure project path changed. Refresh tasks and try again.")
		}
		sprint := boardSprintFromPath(c, row.Path)
		if row.Name != "" {
			sprint.Name = row.Name
		}
		sprint.Current = true
		result = append(result, sprint)
	}
	return result, nil
}

func boardSprintKey(c activityBoardConnection, item activityBoardItem) string {
	if c.Provider != "azure" || item.IterationPath == "" {
		return ""
	}
	return boardExternalKey("azure-sprint", c.Scope.OrganizationID+":"+c.Scope.ProjectID, strings.ToLower(item.IterationPath))
}

func (a *app) boardSprints(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	project := r.URL.Query().Get("projectId")
	a.mu.Lock()
	if project != "" && !a.boardAvailableProject(project) {
		a.mu.Unlock()
		boardFail(w, 404, "Project unavailable.")
		return
	}
	choices := map[string]activityBoardSprint{}
	needsRefresh := false
	sources := []boardSprintSource{}
	seenProjects := map[string]bool{}
	ids := make([]string, 0, len(a.state.ActivityBoardConnections))
	for id := range a.state.ActivityBoardConnections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := a.state.ActivityBoardConnections[id]
		if c.Provider != "azure" || !a.boardAvailableProject(c.ProjectID) || project != "" && c.ProjectID != project {
			continue
		}
		if a.state.ActivityBoardSync[c.ID].SnapshotPending || !c.OnlyAssignedToMe {
			needsRefresh = true
		}
		key := c.Scope.OrganizationID + ":" + c.Scope.ProjectID
		if !seenProjects[key] {
			seenProjects[key] = true
			sources = append(sources, boardSprintSource{connection: c, credential: a.state.ActivityBoardCredentials[c.ID]})
		}
	}
	for _, item := range a.state.ActivityBoardItems {
		c := a.state.ActivityBoardConnections[item.ConnectionID]
		if c.Provider != "azure" || !a.boardAvailableProject(c.ProjectID) || project != "" && c.ProjectID != project {
			continue
		}
		if a.state.ActivityBoardSync[c.ID].SnapshotPending || !c.OnlyAssignedToMe {
			continue
		}
		if item.IterationPath == "" {
			needsRefresh = true
			continue
		}
		sprint := boardSprintFromPath(c, item.IterationPath)
		choices[sprint.ID] = sprint
	}
	revision, projection := a.state.ActivityBoardConfig.Revision, a.state.ActivityBoardConfig.Projection
	a.mu.Unlock()
	// Never present a partially upgraded projection as a complete sprint menu.
	if needsRefresh {
		choices = map[string]activityBoardSprint{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	currentError := ""
	for _, source := range sources {
		c := source.connection
		token, err := boardCredential(source.credential, false)
		var current []activityBoardSprint
		if err == nil {
			current, err = boardAzureCurrentSprints(ctx, c, string(token))
		}
		if err != nil {
			if currentError == "" {
				currentError = "Could not identify the current sprint for " + c.Scope.Organization + " / " + c.Scope.Project + ". " + err.Error()
			}
			continue
		}
		for _, sprint := range current {
			choices[sprint.ID] = sprint
		}
	}
	// Task/filter revisions and removed projects must not publish outdated choices
	// after the external lookup. No provider HTTP runs while holding app.mu.
	a.mu.Lock()
	changed := a.state.ActivityBoardConfig.Revision != revision || a.state.ActivityBoardConfig.Projection != projection
	if !changed {
		for _, source := range sources {
			if !a.boardAvailableProject(source.connection.ProjectID) {
				changed = true
				break
			}
		}
	}
	a.mu.Unlock()
	if changed {
		boardFail(w, 409, "Tasks changed. Retry loading sprints.")
		return
	}
	items := make([]activityBoardSprint, 0, len(choices))
	for _, sprint := range choices {
		items = append(items, sprint)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Current != b.Current {
			return a.Current
		}
		left, right := strings.ToLower(a.Organization+"/"+a.Project+"/"+a.Path), strings.ToLower(b.Organization+"/"+b.Project+"/"+b.Path)
		if left == right {
			return a.ID < b.ID
		}
		return left < right
	})
	respond(w, 200, map[string]any{"items": items, "needsRefresh": needsRefresh, "currentError": currentError})
}
