package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type activityBoardPhase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type activityBoardConfig struct {
	Schema     int                  `json:"schema"`
	Revision   uint64               `json:"revision,string"`
	Projection uint64               `json:"projection,string"`
	Phases     []activityBoardPhase `json:"phases"`
}
type activityBoardScope struct {
	Repository     string `json:"repository,omitempty"` // Canonical owner/name, not an arbitrary URL.
	RepositoryID   string `json:"repositoryId,omitempty"`
	Organization   string `json:"organization,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"`
	Project        string `json:"project,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	WorkItemType   string `json:"workItemType,omitempty"`
}
type activityBoardValue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type activityBoardConnection struct {
	ID               string                          `json:"id"`
	Name             string                          `json:"name"`
	Provider         string                          `json:"provider"`
	ProjectID        string                          `json:"projectId"`
	Scope            activityBoardScope              `json:"scope"`
	Mappings         map[string][]activityBoardValue `json:"mappings"`                   // First value is the outbound priority.
	OnlyAssignedToMe bool                            `json:"onlyAssignedToMe,omitempty"` // Legacy snapshot migration marker; Azure queries always enforce @Me.
}
type activityBoardItem struct {
	Key           string               `json:"key"`
	ConnectionID  string               `json:"connectionId"`
	ExternalID    string               `json:"externalId"`
	Number        string               `json:"number"`
	Title         string               `json:"title"`
	URL           string               `json:"url"`
	Values        []activityBoardValue `json:"values"`
	State         string               `json:"state"`
	Revision      int                  `json:"revision,omitempty"`
	UpdatedAt     string               `json:"updatedAt"`
	ObservedAt    string               `json:"observedAt"`
	Version       string               `json:"version"`
	IterationPath string               `json:"iterationPath,omitempty"`
}
type activityBoardSync struct {
	LastSyncedAt    string `json:"lastSyncedAt,omitempty"`
	Stale           bool   `json:"stale"`
	Error           string `json:"error,omitempty"`
	SnapshotPending bool   `json:"snapshotPending,omitempty"` // Retained snapshot predates mandatory assignment filtering or belongs to another token owner.
}
type activityBoardMove struct {
	OperationID     string `json:"operationId"`
	Key             string `json:"key"`
	ConnectionID    string `json:"connectionId"`
	PhaseID         string `json:"phaseId"`
	ConfigRevision  uint64 `json:"configRevision,string"`
	ObservedVersion string `json:"observedVersion"`
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

func initializeActivityBoardRecords(d *diskState) {
	if d.ActivityBoardConfig.Schema == 0 {
		d.ActivityBoardConfig.Schema = 1
	}
	if d.ActivityBoardConfig.Phases == nil {
		d.ActivityBoardConfig.Phases = []activityBoardPhase{}
	}
	if d.ActivityBoardConnections == nil {
		d.ActivityBoardConnections = map[string]activityBoardConnection{}
	}
	if d.ActivityBoardCredentials == nil {
		d.ActivityBoardCredentials = map[string][]byte{}
	}
	if d.ActivityBoardItems == nil {
		d.ActivityBoardItems = map[string]activityBoardItem{}
	}
	if d.ActivityBoardSync == nil {
		d.ActivityBoardSync = map[string]activityBoardSync{}
	}
	if d.ActivityBoardMoves == nil {
		d.ActivityBoardMoves = map[string]activityBoardMove{}
	}
}

func validateActivityBoardMappings(phases []activityBoardPhase, mappings map[string][]activityBoardValue) error {
	if len(mappings) != len(phases) {
		return errors.New("Every phase needs a mapping in every board connection.")
	}
	used := map[string]bool{}
	for _, p := range phases {
		if len(mappings[p.ID]) == 0 {
			return errors.New("Every phase needs at least one mapped value.")
		}
		for _, v := range mappings[p.ID] {
			if v.ID == "" || v.Name == "" || used[v.ID] {
				return errors.New("A provider value can belong to only one phase, once per connection.")
			}
			used[v.ID] = true
		}
	}
	return nil
}
func boardScopeKey(c activityBoardConnection) string {
	if c.Provider == "github" {
		return "github.com:" + c.Scope.RepositoryID
	}
	return "azure:" + c.Scope.OrganizationID + ":" + c.Scope.ProjectID + ":" + strings.ToLower(c.Scope.WorkItemType)
}
func validateActivityBoardRecords(d *diskState) error {
	if d.ActivityBoardConfig.Schema == 0 && len(d.ActivityBoardConnections) == 0 && len(d.ActivityBoardItems) == 0 {
		return nil
	}
	if d.ActivityBoardConfig.Schema != 1 {
		return errors.New("Unsupported activity board schema.")
	}
	phases, scopes := map[string]bool{}, map[string]bool{}
	for _, p := range d.ActivityBoardConfig.Phases {
		if p.ID == "" || strings.TrimSpace(p.Name) == "" || phases[p.ID] {
			return errors.New("Invalid activity board phase.")
		}
		phases[p.ID] = true
	}
	for id, c := range d.ActivityBoardConnections {
		if id != c.ID || id == "" || d.project(c.ProjectID) == nil || len(d.ActivityBoardCredentials[id]) == 0 {
			return errors.New("Invalid activity board connection.")
		}
		if c.Provider != "github" && c.Provider != "azure" {
			return errors.New("Invalid activity board provider.")
		}
		if c.OnlyAssignedToMe && c.Provider != "azure" {
			return errors.New("PAT owner assignment filtering is available only for Azure connections.")
		}
		if c.Provider == "github" && (c.Scope.RepositoryID == "" || !boardRepositoryPattern.MatchString(c.Scope.Repository)) {
			return errors.New("Invalid board repository.")
		}
		if c.Provider == "azure" && (!boardOrganizationPattern.MatchString(c.Scope.Organization) || c.Scope.OrganizationID == "" || c.Scope.ProjectID == "" || c.Scope.WorkItemType == "") {
			return errors.New("Invalid board Azure scope.")
		}
		key := boardScopeKey(c)
		if scopes[key] {
			return errors.New("Duplicate activity board scope.")
		}
		scopes[key] = true
		if err := validateActivityBoardMappings(d.ActivityBoardConfig.Phases, c.Mappings); err != nil {
			return err
		}
	}
	for key, item := range d.ActivityBoardItems {
		if key != item.Key || item.ExternalID == "" || d.ActivityBoardConnections[item.ConnectionID].ID == "" || item.Version == "" {
			return errors.New("Invalid activity board projection.")
		}
	}
	for id, m := range d.ActivityBoardMoves {
		if id != m.OperationID || id == "" || m.Key == "" {
			return errors.New("Invalid activity board receipt.")
		}
		switch m.Status {
		case "pending", "unconfirmed", "confirmed", "failed":
		default:
			return errors.New("Invalid activity board receipt status.")
		}
		if boardMoveUnresolved(m) && d.ActivityBoardConnections[m.ConnectionID].ID == "" {
			return errors.New("Orphaned activity board intent.")
		}
	}
	return nil
}
func boardMoveUnresolved(m activityBoardMove) bool {
	return m.Status == "pending" || m.Status == "unconfirmed"
}
func boardClassify(item activityBoardItem, c activityBoardConnection) string {
	found := ""
	for phase, values := range c.Mappings {
		for _, v := range values {
			for _, actual := range item.Values {
				if actual.ID == v.ID {
					if found != "" && found != phase {
						return "conflicts"
					}
					found = phase
				}
			}
		}
	}
	if found == "" && c.Provider == "azure" {
		return "unmapped"
	}
	return found
}
func boardItemVersion(item activityBoardItem) string {
	ids := make([]string, 0, len(item.Values))
	for _, v := range item.Values {
		ids = append(ids, v.ID)
	}
	sort.Strings(ids)
	b, _ := json.Marshal([]any{item.ExternalID, item.UpdatedAt, item.Revision, item.State, ids})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func boardExternalKey(provider, namespace, id string) string {
	h := sha256.Sum256([]byte(provider + ":" + namespace + ":" + id))
	return hex.EncodeToString(h[:])
}
func boardRevision(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Invalid board revision.")
	}
	return n, nil
}
