package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type boardGithubLabel struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}
type boardGithubIssue struct {
	ID          json.Number        `json:"id"`
	Number      int                `json:"number"`
	Title       string             `json:"title"`
	State       string             `json:"state"`
	UpdatedAt   string             `json:"updated_at"`
	Labels      []boardGithubLabel `json:"labels"`
	PullRequest json.RawMessage    `json:"pull_request"`
}

func boardGithubPath(c activityBoardConnection) string {
	parts := strings.Split(c.Scope.Repository, "/")
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}
func boardDiscoverGithub(ctx context.Context, c activityBoardConnection, token string) (activityBoardConnection, []activityBoardValue, error) {
	if !boardRepositoryPattern.MatchString(c.Scope.Repository) {
		return c, nil, errors.New("Enter a GitHub repository as owner/name.")
	}
	var repo struct {
		ID       json.Number `json:"id"`
		FullName string      `json:"full_name"`
	}
	if err := boardAPI(ctx, c, token, "GET", boardGithubPath(c), nil, &repo); err != nil {
		return c, nil, err
	}
	if !boardRepositoryPattern.MatchString(repo.FullName) || repo.ID == "" {
		return c, nil, errors.New("Invalid repository identity.")
	}
	if c.Scope.RepositoryID != "" && c.Scope.RepositoryID != repo.ID.String() {
		return c, nil, errors.New("Repository identity changed. Reconnect the scope.")
	}
	c.Scope = activityBoardScope{Repository: repo.FullName, RepositoryID: repo.ID.String()}
	values := []activityBoardValue{}
	for page := 1; ; page++ {
		var labels []boardGithubLabel
		if err := boardAPI(ctx, c, token, "GET", fmt.Sprintf("%s/labels?per_page=100&page=%d", boardGithubPath(c), page), nil, &labels); err != nil {
			return c, nil, err
		}
		for _, l := range labels {
			values = append(values, activityBoardValue{l.ID.String(), l.Name})
		}
		if len(labels) < 100 {
			break
		}
	}
	return c, values, nil
}
func boardGithubProjection(c activityBoardConnection, issue boardGithubIssue) (activityBoardItem, error) {
	if !boardSafeNumber(issue.ID.String()) || issue.Number <= 0 || len(issue.PullRequest) > 0 {
		return activityBoardItem{}, errors.New("Issue is unavailable or no longer belongs to this scope.")
	}
	item := activityBoardItem{Key: boardExternalKey("github", "github.com", issue.ID.String()), ConnectionID: c.ID, ExternalID: issue.ID.String(), Number: strconv.Itoa(issue.Number), Title: issue.Title,
		URL: "https://github.com/" + c.Scope.Repository + "/issues/" + strconv.Itoa(issue.Number), State: issue.State, UpdatedAt: issue.UpdatedAt, ObservedAt: now(), Values: []activityBoardValue{}}
	for _, l := range issue.Labels {
		item.Values = append(item.Values, activityBoardValue{l.ID.String(), l.Name})
	}
	item.Version = boardItemVersion(item)
	return item, nil
}
func boardReadGithubItem(ctx context.Context, c activityBoardConnection, token string, item activityBoardItem) (activityBoardItem, error) {
	if !boardSafeNumber(item.Number) {
		return item, errors.New("Invalid issue locator.")
	}
	var issue boardGithubIssue
	if err := boardAPI(ctx, c, token, "GET", boardGithubPath(c)+"/issues/"+item.Number, nil, &issue); err != nil {
		return item, err
	}
	if issue.ID.String() != item.ExternalID {
		return item, errors.New("Issue identity changed. Refresh the board.")
	}
	return boardGithubProjection(c, issue)
}
func boardScanGithub(ctx context.Context, c activityBoardConnection, token string) (map[string]activityBoardItem, error) {
	items := map[string]activityBoardItem{}
	for page := 1; ; page++ {
		var issues []boardGithubIssue
		path := fmt.Sprintf("%s/issues?state=all&sort=created&direction=asc&per_page=100&page=%d", boardGithubPath(c), page)
		if err := boardAPI(ctx, c, token, "GET", path, nil, &issues); err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if len(issue.PullRequest) > 0 {
				continue
			}
			item, err := boardGithubProjection(c, issue)
			if err != nil {
				return nil, err
			}
			items[item.Key] = item
		}
		if len(issues) < 100 {
			return items, nil
		}
	}
}
func boardMoveGithub(ctx context.Context, c activityBoardConnection, token string, item activityBoardItem, phase string) error {
	target := c.Mappings[phase][0]
	path := boardGithubPath(c) + "/issues/" + item.Number + "/labels"
	// Specific add/remove calls preserve unrelated labels, including concurrent additions.
	if err := boardAPI(ctx, c, token, "POST", path, map[string]any{"labels": []string{target.Name}}, nil); err != nil {
		return err
	}
	flow := map[string]bool{}
	for _, values := range c.Mappings {
		for _, v := range values {
			flow[v.ID] = true
		}
	}
	for _, actual := range item.Values {
		if actual.ID == target.ID || !flow[actual.ID] {
			continue
		}
		if err := boardAPI(ctx, c, token, "DELETE", path+"/"+url.PathEscape(actual.Name), nil, nil); err != nil {
			return err
		}
	}
	return nil
}
