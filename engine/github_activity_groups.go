package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IDs remain decimal strings end-to-end, including values above 2^53.
// Only explicit provider relationships are used. No name/SHA/time joins.
type githubPipelineMetadata struct {
	Kind         string `json:"kind"`
	RepositoryID string `json:"repositoryId"`
	ID           string `json:"id"`
	RunID        string `json:"runId,omitempty"`
	Attempt      string `json:"attempt,omitempty"`
	RunNumber    string `json:"runNumber,omitempty"`
	WorkflowID   string `json:"workflowId,omitempty"`
	WorkflowName string `json:"workflowName,omitempty"`
	SuiteID      string `json:"suiteId,omitempty"`
	CheckURL     string `json:"checkUrl,omitempty"`
	DeploymentID string `json:"deploymentId,omitempty"`
	Environment  string `json:"environment,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Status       string `json:"status,omitempty"`
	Conclusion   string `json:"conclusion,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	StartedAt    string `json:"startedAt,omitempty"`
	CompletedAt  string `json:"completedAt,omitempty"`
}

type githubActivityGroup struct {
	Kind            string               `json:"kind"`
	RunID           string               `json:"runId,omitempty"`
	Attempt         string               `json:"attempt,omitempty"`
	DeploymentID    string               `json:"deploymentId,omitempty"`
	EventCount      int                  `json:"eventCount"`
	ChildCount      int                  `json:"childCount"`
	Children        []githubActivityItem `json:"children"`
	ChildrenHasMore bool                 `json:"childrenHasMore"`
	Filtered        bool                 `json:"filtered"`
	HighWatermark   string               `json:"highWatermark"`
	Composition     string               `json:"composition"`
}

func pipelineID(p map[string]any, path string) string {
	s := activityText(p, path)
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != s {
		return ""
	}
	return s
}

func extractGithubPipeline(kind string, p map[string]any) *githubPipelineMetadata {
	switch kind {
	case "workflow_run", "workflow_job", "check_run", "check_suite", "deployment", "deployment_status":
	default:
		return nil
	}
	m := &githubPipelineMetadata{Kind: kind, RepositoryID: pipelineID(p, "repository.id"), ID: pipelineID(p, kind+".id")}
	o := kind + "."
	m.RunID, m.Attempt = pipelineID(p, o+"run_id"), pipelineID(p, o+"run_attempt")
	if kind == "workflow_run" {
		m.RunID = m.ID
	}
	m.RunNumber, m.WorkflowID = pipelineID(p, o+"run_number"), pipelineID(p, o+"workflow_id")
	m.WorkflowName = activityText(p, o+"workflow_name")
	if kind == "workflow_run" {
		m.WorkflowName = activityText(p, o+"name")
	}
	m.SuiteID = pipelineID(p, o+"check_suite_id")
	if kind == "check_run" {
		m.SuiteID = pipelineID(p, "check_run.check_suite.id")
		m.CheckURL = activityFirstURL(p, "check_run.url")
		m.DeploymentID = pipelineID(p, "check_run.deployment.id")
	}
	if kind == "workflow_job" {
		m.CheckURL = activityFirstURL(p, "workflow_job.check_run_url")
		m.DeploymentID = pipelineID(p, "deployment.id")
	}
	if kind == "deployment" || kind == "deployment_status" {
		m.DeploymentID = pipelineID(p, "deployment.id")
	}
	m.Environment = activityText(p, "deployment.environment")
	m.SHA = activityText(p, o+"head_sha", "deployment.sha")
	m.Ref = activityText(p, o+"head_branch", "deployment.ref")
	m.Status, m.Conclusion = activityText(p, o+"status", o+"state"), activityText(p, o+"conclusion")
	m.CreatedAt, m.UpdatedAt = activityText(p, o+"created_at"), activityText(p, o+"updated_at")
	m.StartedAt, m.CompletedAt = activityText(p, o+"started_at", o+"run_started_at"), activityText(p, o+"completed_at")
	// Never cache arbitrary-length webhook strings as structured metadata.
	for _, v := range []*string{&m.WorkflowName, &m.Environment, &m.SHA, &m.Ref, &m.Status, &m.Conclusion, &m.CreatedAt, &m.UpdatedAt, &m.StartedAt, &m.CompletedAt} {
		truncated := false
		*v = activityBound(*v, 256, &truncated)
	}
	return m
}

func pipelineScope(i githubActivityItem) string {
	return strings.Join([]string{i.ProjectID, i.WebhookID, i.Provider, i.Pipeline.RepositoryID}, "/")
}
func pipelineRunKey(i githubActivityItem) string {
	m := i.Pipeline
	if m == nil || m.RepositoryID == "" || m.RunID == "" || m.Attempt == "" {
		return ""
	}
	return pipelineScope(i) + "/run/" + m.RunID + "/attempt/" + m.Attempt
}
func pipelineCardID(key string) string {
	h := sha256.Sum256([]byte(key))
	return "pipeline-" + hex.EncodeToString(h[:])
}

type githubActivityProjection struct {
	items    []githubActivityItem
	events   map[string][]githubActivityItem
	children map[string][]githubActivityItem
}
type pipelineLink struct{ key, job string }

// Preserve ambiguity rather than picking whichever event happened to be last.
func pipelineAddLink(index map[string]pipelineLink, id string, link pipelineLink) {
	if id == "" {
		return
	}
	old, exists := index[id]
	if !exists {
		index[id] = link
	} else if old != link {
		index[id] = pipelineLink{}
	}
}

func pipelineStateTime(i githubActivityItem) time.Time {
	m := i.Pipeline
	if m == nil {
		return time.Time{}
	}
	for _, stamp := range []string{m.UpdatedAt, m.CompletedAt, m.StartedAt, m.CreatedAt} {
		if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			return t
		}
	}
	return time.Time{}
}
func pipelineTerminal(i githubActivityItem) bool {
	return i.Pipeline.Conclusion != "" || i.Pipeline.Status == "completed"
}
func pipelineNewer(candidate, previous githubActivityItem) bool {
	// Within one immutable run attempt/job, completed cannot regress to queued.
	// Deployment states instead follow provider timestamps (success -> inactive is valid).
	if candidate.Pipeline.Kind != "deployment" && candidate.Pipeline.Kind != "deployment_status" && pipelineTerminal(candidate) != pipelineTerminal(previous) {
		return pipelineTerminal(candidate)
	}
	if candidate.Pipeline.Kind == "deployment_status" && previous.Pipeline.Kind == "deployment" {
		return true
	}
	if candidate.Pipeline.Kind == "deployment" && previous.Pipeline.Kind == "deployment_status" {
		return false
	}
	ct, pt := pipelineStateTime(candidate), pipelineStateTime(previous)
	if !ct.Equal(pt) {
		return ct.After(pt)
	}
	return activitySequence(candidate) > activitySequence(previous)
}
func activitySequence(i githubActivityItem) uint64 {
	n, _ := strconv.ParseUint(i.ReleaseSequence, 10, 64)
	return n
}
func pipelineTone(state string) string {
	switch state {
	case "success", "built":
		return "success"
	case "failure", "error", "timed_out", "errored", "startup_failure":
		return "danger"
	case "queued", "pending", "waiting", "in_progress", "requested":
		return "accent"
	default:
		return "muted"
	}
}

func (a *app) projectGithubActivity(ctx context.Context, sources []githubActivitySource, high uint64, excluded map[string]bool, category, search string) (githubActivityProjection, error) {
	projection := githubActivityProjection{events: map[string][]githubActivityItem{}, children: map[string][]githubActivityItem{}}
	all := make([]githubActivityItem, 0, len(sources))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return projection, err
		}
		if source.release.ReleaseSequence > high {
			continue
		}
		i := a.githubActivity.summary(source.event)
		i.ID, i.DetailEventID, i.ProjectID, i.ProjectName, i.WebhookID = source.event.ID, source.event.ID, source.event.ProjectID, source.projectName, source.event.WebhookID
		i.ReleaseSequence, i.ReceivedAt, i.ExpiresAt, i.EmittedAt = strconv.FormatUint(source.release.ReleaseSequence, 10), source.event.ReceivedAt, source.event.ExpiresAt, source.release.EmittedAt
		all = append(all, i)
	}
	sort.Slice(all, func(i, j int) bool { return activitySequence(all[i]) > activitySequence(all[j]) })
	checks, suites, deployments := map[string]pipelineLink{}, map[string]pipelineLink{}, map[string]pipelineLink{}
	for _, i := range all {
		m := i.Pipeline
		if m == nil || m.RepositoryID == "" {
			continue
		}
		key := pipelineRunKey(i)
		if key == "" {
			continue
		}
		scope := pipelineScope(i)
		if m.Kind == "workflow_job" && m.ID != "" {
			if m.CheckURL != "" {
				pipelineAddLink(checks, scope+"/check/"+m.CheckURL, pipelineLink{key, m.ID})
			}
			if m.DeploymentID != "" {
				pipelineAddLink(deployments, scope+"/deployment/"+m.DeploymentID, pipelineLink{key, m.ID})
			}
		}
		if m.Kind == "workflow_run" && m.SuiteID != "" {
			pipelineAddLink(suites, scope+"/suite/"+m.SuiteID, pipelineLink{key, ""})
		}
	}
	// A check_run can carry a deployment, but must first be proved to be a job.
	for _, i := range all {
		m := i.Pipeline
		if m == nil || m.Kind != "check_run" || m.RepositoryID == "" || m.CheckURL == "" || m.DeploymentID == "" {
			continue
		}
		if link := checks[pipelineScope(i)+"/check/"+m.CheckURL]; link.key != "" {
			pipelineAddLink(deployments, pipelineScope(i)+"/deployment/"+m.DeploymentID, link)
		}
	}
	groups := map[string][]githubActivityItem{}
	identities := map[string]string{}
	hidden := map[string]bool{}
	for _, i := range all {
		key, relation := "", ""
		if m := i.Pipeline; m != nil && m.RepositoryID != "" {
			scope := pipelineScope(i)
			switch m.Kind {
			case "workflow_run", "workflow_job":
				if m.ID != "" {
					key = pipelineRunKey(i)
					relation = m.Kind + ".run_id + run_attempt"
					if m.Kind == "workflow_run" {
						relation = "workflow_run.id + run_attempt"
					}
				}
			case "check_run":
				if m.CheckURL != "" {
					link := checks[scope+"/check/"+m.CheckURL]
					key, relation = link.key, "workflow_job.check_run_url = check_run.url"
				}
			case "check_suite":
				// suite IDs can span reruns. An explicitly supplied attempt is required.
				if m.Attempt != "" && m.ID != "" {
					link := suites[scope+"/suite/"+m.ID]
					if strings.HasSuffix(link.key, "/attempt/"+m.Attempt) {
						key, relation = link.key, "workflow_run.check_suite_id + check_suite.run_attempt"
					}
				}
			case "deployment", "deployment_status":
				if m.DeploymentID != "" {
					key = scope + "/deployment/" + m.DeploymentID
					relation = "deployment.id"
					if link := deployments[key]; link.key != "" {
						key, relation = link.key, "explicit job/check deployment.id; job "+link.job
						if excluded["workflow_job"] {
							relation = "explicit job/check deployment.id"
						}
					}
				}
			}
		}
		if key != "" {
			rawKey := key
			key = pipelineCardID(key)
			identities[key] = rawKey
			i.GroupRelation = relation
		}
		if excluded[i.CatalogID] || category != "" && i.Category != category {
			if key != "" {
				hidden[key] = true
			}
			continue
		}
		if key == "" {
			key = i.ID
		}
		groups[key] = append(groups[key], i)
	}
	for id, members := range groups {
		projection.events[id] = members
		card := members[0]
		if strings.HasPrefix(id, "pipeline-") {
			var state *githubActivityItem
			childStates := map[string]githubActivityItem{}
			g := &githubActivityGroup{Kind: "workflow", EventCount: len(members), Children: []githubActivityItem{}, Filtered: hidden[id], HighWatermark: strconv.FormatUint(high, 10)}
			// Detect retention/removal even when the newest release/count is unchanged.
			composition := sha256.New()
			for _, member := range members {
				composition.Write([]byte(member.ID + "\n" + member.GroupRelation + "\n"))
			}
			g.Composition = hex.EncodeToString(composition.Sum(nil))
			parts := strings.Split(identities[id], "/")
			if len(parts) >= 4 && parts[len(parts)-4] == "run" {
				g.RunID, g.Attempt = parts[len(parts)-3], parts[len(parts)-1]
			}
			for _, i := range members {
				m := i.Pipeline
				if m.Kind == "workflow_run" || m.Kind == "workflow_job" {
					g.RunID, g.Attempt = m.RunID, m.Attempt
				}
				if m.Kind == "workflow_run" || (m.Kind == "deployment" || m.Kind == "deployment_status") && !strings.Contains(i.GroupRelation, "explicit job") {
					if state == nil || pipelineNewer(i, *state) {
						copy := i
						state = &copy
					}
				}
				childKey := ""
				if m.Kind == "workflow_job" {
					childKey = "job/" + m.ID
				}
				if m.Kind == "deployment" || m.Kind == "deployment_status" {
					childKey = "deployment/" + m.DeploymentID
				}
				if childKey != "" {
					old, exists := childStates[childKey]
					if !exists || pipelineNewer(i, old) {
						childStates[childKey] = i
					}
				}
			}
			if g.RunID == "" && state != nil && (state.Pipeline.Kind == "deployment" || state.Pipeline.Kind == "deployment_status") {
				g.Kind = "deployment"
				g.DeploymentID = state.Pipeline.DeploymentID
			}
			// A job-only/filtered workflow has no invented overall workflow result.
			if state != nil {
				card = *state
			} else {
				card.Result, card.Tone, card.ActionLabel = "", "muted", "Workflow activity"
				card.Title = "Workflow run"
				if card.Pipeline.WorkflowName != "" {
					card.Title = card.Pipeline.WorkflowName
				}
			}
			card.ID, card.Group = id, g
			card.ReleaseSequence, card.ReceivedAt, card.EmittedAt = members[0].ReleaseSequence, members[0].ReceivedAt, members[0].EmittedAt
			card.ExpiresAt = ""
			for _, i := range members {
				if i.ExpiresAt != "" && (card.ExpiresAt == "" || i.ExpiresAt < card.ExpiresAt) {
					card.ExpiresAt = i.ExpiresAt
				}
			}
			if g.Kind == "workflow" {
				card.Reference = "Run " + g.RunID
				if state != nil && state.Pipeline.RunNumber != "" {
					card.Reference = "Run #" + state.Pipeline.RunNumber
				}
				if g.Attempt != "" {
					card.Reference += " · Attempt " + g.Attempt
				}
			}
			for _, child := range childStates {
				if g.Kind == "deployment" {
					continue
				}
				projection.children[id] = append(projection.children[id], child)
			}
			sort.Slice(projection.children[id], func(i, j int) bool {
				return activitySequence(projection.children[id][i]) > activitySequence(projection.children[id][j])
			})
			g.ChildCount = len(projection.children[id])
			childBytes := 0
			for _, child := range projection.children[id] {
				data, _ := json.Marshal(child)
				if len(g.Children) >= 8 || childBytes+len(data) > 16<<10 {
					break
				}
				g.Children = append(g.Children, child)
				childBytes += len(data)
			}
			g.ChildrenHasMore = len(g.Children) < g.ChildCount
			if card.Pipeline != nil && state != nil {
				result := card.Pipeline.Conclusion
				if result == "" {
					result = card.Pipeline.Status
				}
				card.Result, card.Tone = activityHuman(result), pipelineTone(result)
			}
		}
		match := search == ""
		for _, i := range members {
			if strings.Contains(strings.ToLower(strings.Join([]string{i.ProjectName, i.Repository, i.Actor, i.Title, i.Summary, i.Reference, i.Branch, i.BaseBranch, i.ActionLabel, i.EventType, "GitHub"}, " ")), search) {
				match = true
			}
		}
		if match {
			projection.items = append(projection.items, card)
		}
	}
	sort.Slice(projection.items, func(i, j int) bool {
		return activitySequence(projection.items[i]) > activitySequence(projection.items[j])
	})
	return projection, nil
}
