package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type AutomationWebhook struct {
	EventType string `json:"eventType"`
	Action    string `json:"action"`
}

func supportedAutomationWebhook(eventType, action string) bool {
	switch eventType {
	case "pull_request":
		return action == "opened" || action == "ready_for_review"
	case "issues":
		switch action {
		case "opened", "closed", "deleted", "edited", "assigned", "milestoned":
			return true
		}
	}
	return false
}

// Missing configuration retains the original trigger, including old snapshots.
func automationWebhookConfig(v Automation) AutomationWebhook {
	if v.Webhook == nil {
		return AutomationWebhook{EventType: "pull_request", Action: "opened"}
	}
	return *v.Webhook
}

const automationTriggerEditBlocked = "Wait for queued runs to finish before changing the activation or GitHub event."

func automationActivationChanged(previous, next Automation) bool {
	return previous.Mode != next.Mode || previous.Mode == "webhook" && automationWebhookConfig(previous) != automationWebhookConfig(next)
}

func (d *diskState) validateAutomationTriggerEdit(previous, next Automation) error {
	if automationActivationChanged(previous, next) {
		for _, trigger := range d.AutomationTriggers {
			if trigger.AutomationID == previous.ID && !trigger.Consumed {
				return errors.New(automationTriggerEditBlocked)
			}
		}
	}
	return nil
}

func matchesAutomationWebhook(v Automation, e WebhookStoredEvent) bool {
	w := automationWebhookConfig(v)
	return e.Provider == "github" && w.EventType == e.EventType && w.Action == e.Action
}

func validateAutomationWebhook(v Automation) error {
	w := automationWebhookConfig(v)
	if !supportedAutomationWebhook(w.EventType, w.Action) {
		return errors.New("Choose a supported GitHub webhook event.")
	}
	unavailable := "{{issue.number}}"
	if w.EventType == "issues" {
		unavailable = "{{pr.number}}"
	}
	check := func(prompt string) error {
		if strings.Contains(prompt, unavailable) {
			return errors.New("This webhook event cannot provide " + unavailable + ". Update the task prompt.")
		}
		return nil
	}
	mode := automationSessionMode(v)
	if mode == "new_per_run" || mode == "reuse_created" && v.SessionID == "" {
		if err := check(v.Prompt); err != nil {
			return err
		}
	}
	if mode != "new_per_run" {
		return check(v.FollowUpPrompt)
	}
	return nil
}

// Only selected, bounded references reach the automation, never the raw payload.
func automationWebhookVariables(e WebhookStoredEvent) map[string]string {
	if e.Provider != "github" || !supportedAutomationWebhook(e.EventType, e.Action) {
		return nil
	}
	var payload struct {
		Number int64 `json:"number"`
		Issue  struct {
			Number      int64           `json:"number"`
			PullRequest json.RawMessage `json:"pull_request"`
		} `json:"issue"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"owner"`
		} `json:"repository"`
	}
	if json.Unmarshal([]byte(e.Payload), &payload) != nil {
		return nil
	}
	number, reference := payload.Number, "pr.number"
	if e.EventType == "issues" {
		if len(payload.Issue.PullRequest) != 0 {
			return nil
		}
		number, reference = payload.Issue.Number, "issue.number"
	}
	if number <= 0 {
		return nil
	}
	org := payload.Organization.Login
	if org == "" && payload.Repository.Owner.Type == "Organization" {
		org = payload.Repository.Owner.Login
	}
	if len(org) > 200 || len(payload.Repository.Name) > 200 {
		return nil
	}
	if name, ok := cleanLabel(payload.Repository.Name, 200); !ok || name != payload.Repository.Name {
		return nil
	}
	if org != "" {
		if name, ok := cleanLabel(org, 200); !ok || name != org {
			return nil
		}
	}
	return map[string]string{reference: strconv.FormatInt(number, 10), "organization.name": org, "repository.name": payload.Repository.Name}
}
