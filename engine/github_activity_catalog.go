package main

// The screenshot's 56 preferences. Push ruleset bypass has a stable preference
// ID, NOT an asserted canonical header: match its documented discriminator.
type githubActivityType struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	DefaultEnabled bool   `json:"defaultEnabled"`
	Category       string `json:"category"`
	object         string
	noun           string
}

var githubActivityCatalog = []githubActivityType{
	{"issue_relates_to", "Related issues", true, "Issues", "issue", "Issue relationship"},
	{"create", "Branch or tag creation", true, "Repository", "", "Reference created"},
	{"delete", "Branch or tag deletion", true, "Repository", "", "Reference deleted"},
	{"branch_protection_configuration", "Branch protection configurations", true, "Repository", "repository", "Branch protection"},
	{"branch_protection_rule", "Branch protection rules", true, "Repository", "rule", "Branch protection rule"},
	{"push_ruleset_bypass", "Bypass requests for push rulesets", true, "Security", "exemption_request", "Push ruleset bypass request"},
	{"bypass_request_secret_scanning", "Bypass requests for secret scanning push protections", true, "Security", "exemption_request", "Secret scanning bypass request"},
	{"check_run", "Check runs", true, "Pipelines", "check_run", "Check run"},
	{"check_suite", "Check suites", true, "Pipelines", "check_suite", "Check suite"},
	{"code_scanning_alert", "Code scanning alerts", true, "Security", "alert", "Code scanning alert"},
	{"member", "Collaborator add, remove, or changed", true, "Repository", "member", "Collaborator"},
	{"commit_comment", "Commit comments", true, "Repository", "comment", "Commit comment"},
	{"dependabot_alert", "Dependabot alerts", true, "Security", "alert", "Dependabot alert"},
	{"deploy_key", "Deploy keys", true, "Repository", "key", "Deploy key"},
	{"deployment_status", "Deployment statuses", true, "Pipelines", "deployment_status", "Deployment"},
	{"deployment", "Deployments", true, "Pipelines", "deployment", "Deployment"},
	{"discussion_comment", "Discussion comments", true, "Repository", "discussion", "Discussion comment"},
	{"discussion", "Discussions", true, "Repository", "discussion", "Discussion"},
	{"dismissal_request_dependabot", "Dismissal requests for Dependabot alerts", true, "Security", "exemption_request", "Dependabot dismissal request"},
	{"dismissal_request_code_scanning", "Dismissal requests for code scanning alerts", true, "Security", "exemption_request", "Code scanning dismissal request"},
	{"dismissal_request_secret_scanning", "Dismissal requests for secret scanning alerts", true, "Security", "exemption_request", "Secret scanning dismissal request"},
	{"fork", "Forks", true, "Repository", "forkee", "Repository forked"},
	{"issue_comment", "Issue comments", true, "Issues", "issue", "Issue comment"},
	{"issue_dependencies", "Issue dependencies", true, "Issues", "blocked_issue", "Issue dependency"},
	{"issues", "Issues", true, "Issues", "issue", "Issue"},
	{"label", "Labels", true, "Issues", "label", "Label"},
	{"merge_group", "Merge groups", true, "PRs", "merge_group", "Merge group"},
	{"meta", "Meta", true, "Repository", "", "Webhook"},
	{"milestone", "Milestones", true, "Issues", "milestone", "Milestone"},
	{"package", "Packages", true, "Repository", "package", "Package"},
	{"page_build", "Page builds", true, "Pipelines", "build", "Page build"},
	{"pull_request_review_comment", "Pull request review comments", true, "PRs", "pull_request", "Review comment"},
	{"pull_request_review_thread", "Pull request review threads", true, "PRs", "pull_request", "Review thread"},
	{"pull_request_review", "Pull request reviews", true, "PRs", "pull_request", "Pull request review"},
	{"pull_request", "Pull requests", true, "PRs", "pull_request", "Pull request"},
	{"push", "Pushes", true, "Repository", "head_commit", "Push received"},
	{"registry_package", "Registry packages", true, "Repository", "registry_package", "Registry package"},
	{"release", "Releases", true, "Repository", "release", "Release"},
	{"repository", "Repositories", true, "Repository", "repository", "Repository"},
	{"repository_advisory", "Repository advisories", true, "Security", "repository_advisory", "Repository advisory"},
	{"repository_import", "Repository imports", true, "Repository", "repository", "Repository import"},
	{"repository_ruleset", "Repository rulesets", true, "Repository", "repository_ruleset", "Repository ruleset"},
	{"repository_vulnerability_alert", "Repository vulnerability alerts", true, "Security", "alert", "Repository vulnerability alert"},
	{"secret_scanning_alert_location", "Secret scanning alert locations", true, "Security", "alert", "Secret scanning alert location"},
	{"secret_scanning_alert", "Secret scanning alerts", true, "Security", "alert", "Secret scanning alert"},
	{"secret_scanning_scan", "Secret scanning scans", true, "Security", "", "Secret scanning scan"},
	{"security_and_analysis", "Security and analyses", true, "Security", "repository", "Security and analysis changed"},
	{"star", "Stars", true, "Repository", "repository", "Star"},
	{"status", "Statuses", true, "Pipelines", "commit", "Commit status"},
	{"sub_issues", "Sub issues", true, "Issues", "sub_issue", "Sub-issue relationship"},
	{"team_add", "Team adds", true, "Repository", "team", "Team added"},
	{"public", "Visibility changes", true, "Repository", "repository", "Repository made public"},
	{"watch", "Watches", true, "Repository", "repository", "Watch event"},
	{"gollum", "Wiki", true, "Repository", "", "Wiki updated"},
	{"workflow_job", "Workflow jobs", true, "Pipelines", "workflow_job", "Workflow job"},
	{"workflow_run", "Workflow runs", true, "Pipelines", "workflow_run", "Workflow run"},
}

func githubActivityTypeFor(eventType string, payload map[string]any) githubActivityType {
	for _, item := range githubActivityCatalog {
		if item.ID != "push_ruleset_bypass" && item.ID == eventType {
			return item
		}
	}
	if activityText(payload, "exemption_request.request_type") == "push_ruleset_bypass" {
		return githubActivityCatalog[5]
	}
	return githubActivityType{Category: "Other", noun: "GitHub event received"}
}
