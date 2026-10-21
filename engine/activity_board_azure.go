package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func boardAzureProjectPath(c activityBoardConnection) string {
	return "/" + url.PathEscape(c.Scope.ProjectID)
}

// Setup-only catalog reads. Draft scopes and credentials are never persisted.
func boardDiscoverAzureOptions(ctx context.Context, c activityBoardConnection, token, discovery string) (activityBoardConnection, []activityBoardValue, error) {
	if !boardOrganizationPattern.MatchString(c.Scope.Organization) {
		return c, nil, errors.New("Enter the Azure organization name.")
	}
	values := []activityBoardValue{}
	if discovery == "projects" {
		seen := map[string]bool{}
		// Projects List supports $top/$skip; read every page before publishing options.
		for skip := 0; ; {
			var page struct {
				Value []activityBoardValue `json:"value"`
			}
			path := fmt.Sprintf("/_apis/projects?stateFilter=wellFormed&$top=100&$skip=%d&api-version=7.1", skip)
			if err := boardAPI(ctx, c, token, "GET", path, nil, &page); err != nil {
				var pe *boardProviderError
				if errors.As(err, &pe) && (pe.status == 401 || pe.status == 403) {
					return c, nil, errors.New("Azure denied project discovery. Check organization access and the token's Project and Team (Read) permission.")
				}
				return c, nil, err
			}
			if page.Value == nil {
				return c, nil, errors.New("Azure returned an invalid project catalog.")
			}
			added := 0
			for _, p := range page.Value {
				if p.ID == "" || p.Name == "" {
					return c, nil, errors.New("Azure returned an invalid project catalog.")
				}
				if !seen[p.ID] {
					seen[p.ID] = true
					values = append(values, p)
					added++
				}
			}
			if len(page.Value) < 100 {
				break
			}
			if added == 0 {
				return c, nil, errors.New("Azure project catalog is incomplete. Load projects again.")
			}
			skip += len(page.Value)
		}
	} else {
		locator := c.Scope.ProjectID
		if locator == "" {
			locator = c.Scope.Project
		}
		if strings.TrimSpace(locator) == "" {
			return c, nil, errors.New("Choose an Azure project first.")
		}
		var project activityBoardValue
		if err := boardAPI(ctx, c, token, "GET", "/_apis/projects/"+url.PathEscape(locator)+"?api-version=7.1", nil, &project); err != nil {
			return c, nil, err
		}
		if project.ID == "" || project.Name == "" || c.Scope.ProjectID != "" && c.Scope.ProjectID != project.ID {
			return c, nil, errors.New("Azure project identity changed. Load projects again.")
		}
		c.Scope.ProjectID, c.Scope.Project = project.ID, project.Name
		var types struct {
			Value []struct {
				Name string `json:"name"`
			} `json:"value"`
		}
		if err := boardAPI(ctx, c, token, "GET", boardAzureProjectPath(c)+"/_apis/wit/workitemtypes?api-version=7.1", nil, &types); err != nil {
			return c, nil, err
		}
		if types.Value == nil {
			return c, nil, errors.New("Azure returned an invalid work item type catalog.")
		}
		seen := map[string]bool{}
		for _, kind := range types.Value {
			if kind.Name == "" {
				return c, nil, errors.New("Azure returned an invalid work item type catalog.")
			}
			if !seen[kind.Name] {
				seen[kind.Name] = true
				values = append(values, activityBoardValue{kind.Name, kind.Name})
			}
		}
	}
	sort.Slice(values, func(i, j int) bool {
		a, b := strings.ToLower(values[i].Name), strings.ToLower(values[j].Name)
		if a == b {
			return values[i].ID < values[j].ID
		}
		return a < b
	})
	return c, values, nil
}

func boardDiscoverAzure(ctx context.Context, c activityBoardConnection, token string) (activityBoardConnection, []activityBoardValue, error) {
	if !boardOrganizationPattern.MatchString(c.Scope.Organization) || strings.TrimSpace(c.Scope.Project) == "" || strings.TrimSpace(c.Scope.WorkItemType) == "" {
		return c, nil, errors.New("Azure organization, project and work item type are required.")
	}
	var identity struct {
		InstanceID string `json:"instanceId"`
	}
	if err := boardAPI(ctx, c, token, "GET", "/_apis/connectionData?api-version=7.1-preview.1", nil, &identity); err != nil {
		return c, nil, err
	}
	var project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	projectLocator := c.Scope.ProjectID
	if projectLocator == "" {
		projectLocator = c.Scope.Project
	}
	if err := boardAPI(ctx, c, token, "GET", "/_apis/projects/"+url.PathEscape(projectLocator)+"?api-version=7.1", nil, &project); err != nil {
		return c, nil, err
	}
	if identity.InstanceID == "" || project.ID == "" {
		return c, nil, errors.New("Azure scope identity unavailable.")
	}
	if c.Scope.OrganizationID != "" && (c.Scope.OrganizationID != identity.InstanceID || c.Scope.ProjectID != project.ID) {
		return c, nil, errors.New("Azure scope identity changed. Reconnect the scope.")
	}
	c.Scope.OrganizationID, c.Scope.ProjectID, c.Scope.Project = identity.InstanceID, project.ID, project.Name
	var kind struct {
		Name string `json:"name"`
	}
	typePath := boardAzureProjectPath(c) + "/_apis/wit/workitemtypes/" + url.PathEscape(c.Scope.WorkItemType)
	if err := boardAPI(ctx, c, token, "GET", typePath+"?api-version=7.1", nil, &kind); err != nil {
		return c, nil, err
	}
	if kind.Name == "" {
		return c, nil, errors.New("Azure work item type unavailable.")
	}
	c.Scope.WorkItemType = kind.Name
	var states struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := boardAPI(ctx, c, token, "GET", typePath+"/states?api-version=7.1", nil, &states); err != nil {
		return c, nil, err
	}
	values := []activityBoardValue{}
	for _, s := range states.Value {
		if s.Name != "" {
			values = append(values, activityBoardValue{s.Name, s.Name})
		}
	}
	return c, values, nil
}

type boardAzureItem struct {
	ID     int            `json:"id"`
	Rev    int            `json:"rev"`
	Fields map[string]any `json:"fields"`
}

const boardAzureFields = "System.Id,System.Title,System.State,System.WorkItemType,System.TeamProject,System.ChangedDate,System.IterationPath"

func boardAzureProjection(c activityBoardConnection, wi boardAzureItem) (activityBoardItem, error) {
	text := func(key string) string { s, _ := wi.Fields[key].(string); return s }
	if wi.ID <= 0 || wi.Rev < 1 || text("System.TeamProject") != c.Scope.Project || text("System.WorkItemType") != c.Scope.WorkItemType {
		return activityBoardItem{}, errors.New("Work item moved outside this connection. Refresh the board.")
	}
	id, state := strconv.Itoa(wi.ID), text("System.State")
	item := activityBoardItem{Key: boardExternalKey("azure", c.Scope.OrganizationID, id), ExternalID: id, ConnectionID: c.ID, Number: id, Title: text("System.Title"),
		URL:   "https://dev.azure.com/" + url.PathEscape(c.Scope.Organization) + "/" + url.PathEscape(c.Scope.ProjectID) + "/_workitems/edit/" + id,
		State: state, Values: []activityBoardValue{{state, state}}, Revision: wi.Rev, UpdatedAt: text("System.ChangedDate"), ObservedAt: now()}
	item.IterationPath = text("System.IterationPath")
	if item.IterationPath == "" {
		return activityBoardItem{}, errors.New("Azure returned a work item without its iteration path. Refresh the tasks.")
	}
	item.Version = boardItemVersion(item)
	return item, nil
}
func boardReadAzureItem(ctx context.Context, c activityBoardConnection, token string, item activityBoardItem) (activityBoardItem, error) {
	if !boardSafeNumber(item.ExternalID) {
		return item, errors.New("Invalid work item locator.")
	}
	var wi boardAzureItem
	if err := boardAPI(ctx, c, token, "GET", "/_apis/wit/workitems/"+item.ExternalID+"?fields="+url.QueryEscape(boardAzureFields)+"&api-version=7.1", nil, &wi); err != nil {
		return item, err
	}
	// @Me is always resolved by Azure from this connection's PAT. Never use a
	// client preference, name/email comparison or legacy flag to bypass this check.
	var matches struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	query := "SELECT [System.Id] FROM WorkItems WHERE [System.Id] = " + item.ExternalID + " AND [System.AssignedTo] = @Me"
	if err := boardAPI(ctx, c, token, "POST", boardAzureProjectPath(c)+"/_apis/wit/wiql?api-version=7.1", map[string]string{"query": query}, &matches); err != nil {
		return item, err
	}
	if len(matches.WorkItems) != 1 || matches.WorkItems[0].ID != wi.ID {
		return item, errors.New("Work item is no longer assigned to the PAT owner. Refresh the tasks.")
	}
	return boardAzureProjection(c, wi)
}
func boardScanAzure(ctx context.Context, c activityBoardConnection, token string) (map[string]activityBoardItem, error) {
	items, last := map[string]activityBoardItem{}, 0
	quote := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	for {
		var page struct {
			WorkItems []struct {
				ID int `json:"id"`
			} `json:"workItems"`
		}
		query := fmt.Sprintf("SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = '%s' AND [System.WorkItemType] = '%s' AND [System.AssignedTo] = @Me AND [System.Id] > %d ORDER BY [System.Id] ASC", quote(c.Scope.Project), quote(c.Scope.WorkItemType), last)
		if err := boardAPI(ctx, c, token, "POST", boardAzureProjectPath(c)+"/_apis/wit/wiql?$top=200&api-version=7.1", map[string]string{"query": query}, &page); err != nil {
			return nil, err
		}
		if len(page.WorkItems) == 0 {
			return items, nil
		}
		ids := []string{}
		for _, ref := range page.WorkItems {
			if ref.ID <= last {
				return nil, errors.New("Azure inventory ordering incomplete. Snapshot was not replaced.")
			}
			last = ref.ID
			ids = append(ids, strconv.Itoa(ref.ID))
		}
		var batch struct {
			Value []boardAzureItem `json:"value"`
		}
		if err := boardAPI(ctx, c, token, "GET", "/_apis/wit/workitems?ids="+strings.Join(ids, ",")+"&fields="+url.QueryEscape(boardAzureFields)+"&errorPolicy=fail&api-version=7.1", nil, &batch); err != nil {
			return nil, err
		}
		if len(batch.Value) != len(ids) {
			return nil, errors.New("Azure inventory incomplete. Snapshot was not replaced.")
		}
		for _, wi := range batch.Value {
			item, err := boardAzureProjection(c, wi)
			if err != nil {
				return nil, err
			}
			items[item.Key] = item
		}
		if len(page.WorkItems) < 200 {
			return items, nil
		}
	}
}
func boardMoveAzure(ctx context.Context, c activityBoardConnection, token string, item activityBoardItem, phase string) error {
	body := []map[string]any{{"op": "test", "path": "/rev", "value": item.Revision}, {"op": "add", "path": "/fields/System.State", "value": c.Mappings[phase][0].Name}}
	return boardAPI(ctx, c, token, "PATCH", "/_apis/wit/workitems/"+item.ExternalID+"?api-version=7.1", body, nil)
}
