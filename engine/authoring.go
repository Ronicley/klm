package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

type AgentDefinition struct {
	Name           string `json:"name" toml:"name"`
	Description    string `json:"description" toml:"description,omitempty"`
	Enabled        bool   `json:"enabled" toml:"enabled"`
	DefaultHarness string `json:"defaultHarness" toml:"default_harness"`
	Model          string `json:"model" toml:"model"`
	Effort         string `json:"effort,omitempty" toml:"effort,omitempty"`
	Prompt         string `json:"prompt" toml:"prompt,multiline"`
}
type AgentRecord struct {
	AgentDefinition
	ID              string   `json:"id"`
	Revision        string   `json:"revision"`
	GraphReferences []string `json:"graphReferences"`
}
type NodeOverrides struct {
	Harness string  `json:"harness,omitempty" yaml:"harness,omitempty"`
	Model   string  `json:"model,omitempty" yaml:"model,omitempty"`
	Effort  *string `json:"effort,omitempty" yaml:"effort,omitempty"`
}
type GraphNode struct {
	Type         string                 `json:"type" yaml:"type"`
	Name         string                 `json:"name,omitempty" yaml:"name,omitempty"`
	Agent        string                 `json:"agent,omitempty" yaml:"agent,omitempty"`
	Overrides    *NodeOverrides         `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	Choices      []string               `json:"choices,omitempty" yaml:"choices,omitempty"`
	Command      string                 `json:"command,omitempty" yaml:"command,omitempty"`
	Output       map[string]string      `json:"output,omitempty" yaml:"output,omitempty"`
	To           string                 `json:"to,omitempty" yaml:"to,omitempty"`
	Branches     map[string]GraphBranch `json:"branches,omitempty" yaml:"branches,omitempty"`
	Prompt       string                 `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	OutputBranch string                 `json:"output_branch,omitempty" yaml:"output_branch,omitempty"`
}
type GraphBranch struct {
	SeparateWorktree *bool             `json:"separate_worktree,omitempty" yaml:"separate_worktree,omitempty"`
	Name             string            `json:"name" yaml:"name"`
	GitBranch        string            `json:"git_branch" yaml:"git_branch"`
	Output           map[string]string `json:"output" yaml:"output"`
	To               string            `json:"to" yaml:"to"`
}

// Omitted in existing YAML/JSON means isolated, including after a round trip.
func (b GraphBranch) Isolated() bool { return b.SeparateWorktree == nil || *b.SeparateWorktree }

type ChoiceField struct {
	Type     string `json:"type" yaml:"type"`
	Required bool   `json:"required" yaml:"required"`
}
type GraphChoice struct {
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Input       map[string]ChoiceField `json:"input" yaml:"input"`
	Output      map[string]string      `json:"output" yaml:"output"`
	To          string                 `json:"to,omitempty" yaml:"to,omitempty"`
	Session     string                 `json:"session,omitempty" yaml:"session,omitempty"`
	Terminal    bool                   `json:"terminal,omitempty" yaml:"terminal,omitempty"`
}
type GraphDefinition struct {
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description" yaml:"description,omitempty"`
	Enabled     bool                   `json:"enabled" yaml:"enabled"`
	InitialNode string                 `json:"initial_node" yaml:"initial_node"`
	Nodes       map[string]GraphNode   `json:"nodes" yaml:"nodes"`
	Choices     map[string]GraphChoice `json:"choices" yaml:"choices"`
}
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}
type GraphLayout struct {
	Positions map[string]Point `json:"positions"`
	Viewport  *Viewport        `json:"viewport,omitempty"`
}
type GraphRecord struct {
	ID         string          `json:"id"`
	Revision   string          `json:"revision"`
	Definition GraphDefinition `json:"definition"`
	Layout     GraphLayout     `json:"layout"`
}
type AuthoringCatalog struct {
	Agents []AgentRecord `json:"agents"`
	Graphs []GraphRecord `json:"graphs"`
	Errors []string      `json:"errors"`
}

func authoringSlug(name string) string {
	var b strings.Builder
	separator := false
	for _, r := range norm.NFKD.String(strings.ToLower(strings.TrimSpace(name))) {
		if unicode.Is(unicode.M, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	return b.String()
}
func validAuthoringID(id string) bool {
	if id == "" || len(id) > 200 || id != authoringSlug(id) {
		return false
	}
	base := strings.ToUpper(id)
	return base != "CON" && base != "PRN" && base != "AUX" && base != "NUL" && !regexp.MustCompile(`^(COM|LPT)[0-9]$`).MatchString(base)
}
func revision(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }

func (a *app) authoringProject(w http.ResponseWriter, r *http.Request) (Project, bool) {
	a.mu.Lock()
	p := a.state.project(r.PathValue("id"))
	if p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return Project{}, false
	}
	project := *p
	a.mu.Unlock()
	return project, true
}

// Never follow project configuration directories/files outside the project. Reads
// and writes accept only generated relative filenames, not paths from the client.
func configPath(project Project, relative string, create bool) (string, error) {
	root, err := filepath.EvalSymlinks(project.Folder)
	if err != nil {
		return "", errors.New("Project directory is unavailable.")
	}
	path := root
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\:`) {
			return "", errors.New("Invalid configuration path.")
		}
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if create && i < len(parts)-1 {
				if err = os.Mkdir(path, 0755); err != nil && !errors.Is(err, os.ErrExist) {
					return "", err
				}
			}
			continue
		}
		if err != nil {
			return "", err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
			return "", errors.New("Linked configuration files and directories are not supported.")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", errors.New("Configuration parent is not a directory.")
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", errors.New("Configuration path is not a regular file.")
		}
	}
	return path, nil
}
func readConfig(p Project, name string) ([]byte, error) {
	path, err := configPath(p, name, false)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if len(b) > 2<<20 {
		return nil, errors.New("Configuration file exceeds 2 MiB.")
	}
	return b, err
}
func writeConfig(p Project, name string, data []byte) error {
	path, err := configPath(p, name, true)
	if err != nil {
		return err
	}
	if data == nil {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".authoring-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(f.Name(), path)
}

// A small roll-forward journal keeps agent renames and graph references consistent
// across failed writes/restarts. All authoring readers recover it under the mutex.
const authoringJournal = ".klm/.authoring-transaction.json"

func recoverAuthoring(p Project) error {
	b, err := readConfig(p, authoringJournal)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var writes map[string][]byte
	if err = json.Unmarshal(b, &writes); err != nil {
		return errors.New("Invalid pending authoring transaction.")
	}
	keys := make([]string, 0, len(writes))
	for name := range writes {
		if !(strings.HasPrefix(name, ".klm/agents/") && strings.HasSuffix(name, ".toml") || strings.HasPrefix(name, ".klm/graphs/") && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yaml.layout.json"))) {
			return errors.New("Invalid authoring transaction path.")
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if err := writeConfig(p, name, writes[name]); err != nil {
			return err
		}
	}
	return writeConfig(p, authoringJournal, nil)
}
func commitAuthoring(p Project, writes map[string][]byte) error {
	for name := range writes {
		if _, err := configPath(p, name, true); err != nil {
			return err
		}
	}
	b, err := json.Marshal(writes)
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return errors.New("Authoring transaction is too large.")
	}
	if err = writeConfig(p, authoringJournal, b); err != nil {
		return err
	}
	return recoverAuthoring(p)
}
func agentFile(id string) string  { return ".klm/agents/" + id + ".toml" }
func graphFile(id string) string  { return ".klm/graphs/" + id + ".yaml" }
func layoutFile(id string) string { return graphFile(id) + ".layout.json" }

func loadAuthoring(p Project) (AuthoringCatalog, error) {
	result := AuthoringCatalog{Agents: []AgentRecord{}, Graphs: []GraphRecord{}, Errors: []string{}}
	if err := recoverAuthoring(p); err != nil {
		return result, err
	}
	for _, kind := range []string{"agents", "graphs"} {
		probe, err := configPath(p, ".klm/"+kind+"/.probe", false)
		if err != nil {
			return result, err
		}
		entries, err := os.ReadDir(filepath.Dir(probe))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		for _, entry := range entries {
			ext := ".toml"
			if kind == "graphs" {
				ext = ".yaml"
			}
			if !strings.HasSuffix(entry.Name(), ext) {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ext)
			name := ".klm/" + kind + "/" + entry.Name()
			b, err := readConfig(p, name)
			if err == nil && !validAuthoringID(id) {
				err = errors.New("Invalid filename identifier.")
			}
			if err == nil && kind == "agents" {
				agent := AgentDefinition{Enabled: true}
				err = toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&agent)
				if err == nil && authoringSlug(agent.Name) != id {
					err = errors.New("Agent name and filename identifier differ.")
				}
				if err == nil {
					result.Agents = append(result.Agents, AgentRecord{agent, id, revision(b), []string{}})
				}
			} else if err == nil {
				definition := GraphDefinition{Enabled: true}
				d := yaml.NewDecoder(bytes.NewReader(b))
				d.KnownFields(true)
				err = d.Decode(&definition)
				if err == nil {
					var extra any
					if d.Decode(&extra) != io.EOF {
						err = errors.New("Expected one YAML document.")
					}
				}
				if err == nil && (definition.Name == "" || definition.Nodes == nil || definition.Choices == nil) {
					err = errors.New("Incomplete graph definition.")
				}
				if err == nil {
					layout := GraphLayout{Positions: map[string]Point{}}
					lb, le := readConfig(p, layoutFile(id))
					if le == nil {
						le = json.Unmarshal(lb, &layout)
						if le == nil {
							le = validateLayout(layout)
						}
					}
					if le != nil && !errors.Is(le, os.ErrNotExist) {
						result.Errors = append(result.Errors, layoutFile(id)+": invalid or unreadable layout; using default positions.")
						layout = GraphLayout{Positions: map[string]Point{}}
					}
					result.Graphs = append(result.Graphs, GraphRecord{id, revision(b), definition, layout})
				}
			}
			if err != nil {
				result.Errors = append(result.Errors, name+": "+err.Error())
			}
		}
	}
	for i := range result.Agents {
		for _, graph := range result.Graphs {
			for _, node := range graph.Definition.Nodes {
				if node.Agent == result.Agents[i].ID {
					result.Agents[i].GraphReferences = append(result.Agents[i].GraphReferences, graph.Definition.Name)
					break
				}
			}
		}
	}
	return result, nil
}
func (a *app) authoring(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := loadAuthoring(p)
	if err != nil {
		fail(w, 500, "Cannot read project configuration: "+err.Error())
		return
	}
	respond(w, 200, c)
}
func (a *app) projectModels(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	c, err := a.catalog(r.Context(), Session{ProjectID: p.ID, Harness: r.PathValue("harness")}, p.Folder, r.URL.Query().Get("refresh") == "true")
	if err != nil {
		fail(w, 502, "Could not load harness models. Check the harness configuration and retry.")
		return
	}
	respond(w, 200, c)
}
func (a *app) validateAgentModel(r *http.Request, p Project, harness, model, effort string) error {
	c, err := a.catalog(r.Context(), Session{ProjectID: p.ID, Harness: harness}, p.Folder, false)
	if err != nil {
		return errors.New("Could not validate model against the harness catalog.")
	}
	m := c.find(model)
	if m == nil {
		return errors.New("Select a model available in the selected harness.")
	}
	if len(m.Efforts) > 0 && !slices.Contains(m.Efforts, effort) {
		return errors.New("Select a supported effort level for this model.")
	}
	if len(m.Efforts) == 0 && effort != "" {
		return errors.New("This model does not support configurable effort.")
	}
	return nil
}
func catalogForMutation(p Project) (AuthoringCatalog, error) {
	c, err := loadAuthoring(p)
	if err == nil && len(c.Errors) > 0 {
		err = errors.New("Resolve configuration errors before modifying the catalog: " + strings.Join(c.Errors, "; "))
	}
	return c, err
}
func (a *app) saveAgent(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Agent    AgentDefinition `json:"agent"`
		Revision string          `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	d := body.Agent
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	id := authoringSlug(d.Name)
	if _, ok := cleanLabel(d.Name, 80); !ok || !validAuthoringID(id) || len(d.Description) > 960 || strings.TrimSpace(d.Prompt) == "" || len(d.Prompt) > 128<<10 {
		fail(w, 400, "Enter a valid name and prompt (at most 128 KiB).")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	if err := a.validateAgentModel(r, p, d.DefaultHarness, d.Model, d.Effort); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	oldID := r.PathValue("item")
	var old *AgentRecord
	for i := range c.Agents {
		if c.Agents[i].ID == oldID {
			old = &c.Agents[i]
		}
		if strings.EqualFold(c.Agents[i].ID, id) && c.Agents[i].ID != oldID {
			fail(w, 409, "An agent with this identifier already exists.")
			return
		}
	}
	if oldID != "" && (old == nil || old.Revision != body.Revision) {
		fail(w, 409, "Agent changed or was removed. Reload before saving.")
		return
	}
	if id != oldID {
		if _, err := readConfig(p, agentFile(id)); !errors.Is(err, os.ErrNotExist) {
			fail(w, 409, "An agent file with this identifier already exists.")
			return
		}
	}
	if old != nil && !d.Enabled && len(old.GraphReferences) > 0 {
		fail(w, 409, "This agent is referenced by a graph and cannot be disabled.")
		return
	}
	b, err := toml.Marshal(d)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writes := map[string][]byte{agentFile(id): b}
	if oldID != "" && oldID != id {
		writes[agentFile(oldID)] = nil
		for _, g := range c.Graphs {
			changed := false
			for key, node := range g.Definition.Nodes {
				if node.Agent == oldID {
					node.Agent = id
					g.Definition.Nodes[key] = node
					changed = true
				}
			}
			if changed {
				data, err := yaml.Marshal(g.Definition)
				if err != nil {
					fail(w, 400, err.Error())
					return
				}
				writes[graphFile(g.ID)] = data
			}
		}
	}
	if err = commitAuthoring(p, writes); err != nil {
		fail(w, 500, "Could not finish saving agent: "+err.Error())
		return
	}
	refs := []string{}
	if old != nil {
		refs = old.GraphReferences
	}
	respond(w, 200, AgentRecord{d, id, revision(b), refs})
}
func (a *app) deleteAgent(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	var body struct {
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	for _, agent := range c.Agents {
		if agent.ID == r.PathValue("item") {
			if agent.Revision != body.Revision {
				fail(w, 409, "Agent changed. Reload before deleting.")
				return
			}
			if len(agent.GraphReferences) > 0 {
				fail(w, 409, "This agent is referenced by a graph and cannot be deleted.")
				return
			}
			if err := commitAuthoring(p, map[string][]byte{agentFile(agent.ID): nil}); err != nil {
				fail(w, 500, err.Error())
				return
			}
			respond(w, 200, map[string]bool{"removed": true})
			return
		}
	}
	fail(w, 404, "Agent not found.")
}

var fieldNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[a-z0-9_]*[a-z0-9])?$`)
var templatePattern = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

func validateOutput(output map[string]string, input map[string]ChoiceField, fork bool) error {
	kind := "choice"
	if fork {
		kind = "fork"
	}
	_, err := compileOutput(output, kind, input)
	return err
}
func validGitBranch(name string) bool {
	return name != "" && name != "@" && name != "HEAD" && !strings.HasPrefix(name, "-") && !strings.HasSuffix(name, ".") && !strings.ContainsAny(name, " ~^:?*[\\\t\r\n") && !strings.Contains(name, "..") && !strings.Contains(name, "@{") && !strings.Contains(name, "//") && !strings.HasPrefix(name, "/") && !strings.HasSuffix(name, "/") && !slices.ContainsFunc(strings.Split(name, "/"), func(s string) bool { return strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".lock") })
}
func validateGraph(g GraphDefinition, agents []AgentRecord) error {
	if _, ok := cleanLabel(g.Name, 80); !ok || !validAuthoringID(authoringSlug(g.Name)) {
		return errors.New("Enter a valid graph name.")
	}
	initial, ok := g.Nodes[g.InitialNode]
	if !ok || initial.Type != "agent" && initial.Type != "terminal" {
		return errors.New("Select an initial agent or terminal node.")
	}
	agentIDs := map[string]bool{}
	for _, a := range agents {
		agentIDs[a.ID] = a.Enabled
	}
	for id, n := range g.Nodes {
		if !validAuthoringID(id) {
			return errors.New("Invalid node identifier.")
		}
		switch n.Type {
		case "agent", "join":
			if !agentIDs[n.Agent] {
				return fmt.Errorf("Node %s needs an enabled agent.", id)
			}
			if n.Type == "agent" && strings.TrimSpace(n.Name) == "" {
				return errors.New("Each agent node needs a name.")
			}
			if n.Command != "" || n.To != "" || len(n.Branches) > 0 || len(n.Output) > 0 {
				return errors.New("Agent/Join nodes communicate through Choices.")
			}
			if n.Type == "agent" && (n.Prompt != "" || n.OutputBranch != "") {
				return errors.New("Integration fields belong to Join nodes.")
			}
			if n.Type == "join" && n.OutputBranch != "" && !validGitBranch(n.OutputBranch) {
				return errors.New("Invalid Join output Git branch.")
			}
			seen := map[string]bool{}
			for _, choice := range n.Choices {
				if _, ok := g.Choices[choice]; !ok || seen[choice] {
					return fmt.Errorf("Invalid Choice reference on node %s.", id)
				}
				seen[choice] = true
			}
		case "terminal", "fork":
			if strings.TrimSpace(n.Name) == "" {
				return errors.New("Terminal and Fork nodes need names.")
			}
			if n.Agent != "" || n.Overrides != nil || len(n.Choices) > 0 || n.Prompt != "" || n.OutputBranch != "" {
				return errors.New("Terminal/Fork nodes cannot select agents or Choices.")
			}
			if n.Type == "terminal" {
				if _, err := compileOutput(n.Output, "terminal", nil); err != nil {
					return err
				}
				if strings.TrimSpace(n.Command) == "" || len(n.Branches) > 0 {
					return errors.New("Terminal nodes need a command.")
				}
				if n.To != "" {
					if _, ok := g.Nodes[n.To]; !ok {
						return errors.New("Invalid terminal destination.")
					}
				}
			} else {
				if n.Command != "" || n.To != "" || len(n.Output) > 0 {
					return errors.New("Fork destinations belong to branches.")
				}
				names := map[string]bool{}
				git := map[string]bool{}
				for branchID, branch := range n.Branches {
					if !validAuthoringID(branchID) || strings.TrimSpace(branch.Name) == "" || names[branch.Name] || (branch.Isolated() && (!validGitBranch(branch.GitBranch) || git[branch.GitBranch])) {
						return errors.New("Fork branches need unique names and valid, distinct Git branches.")
					}
					names[branch.Name] = true
					if branch.Isolated() {
						git[branch.GitBranch] = true
					}
					if branch.To != "" {
						if _, ok := g.Nodes[branch.To]; !ok {
							return errors.New("Invalid Fork destination.")
						}
					}
					if err := validateOutput(branch.Output, nil, true); err != nil {
						return err
					}
				}
			}
		default:
			return errors.New("Unknown node type.")
		}
	}
	for id, c := range g.Choices {
		if !fieldNamePattern.MatchString(c.Name) || id != authoringSlug(c.Name) {
			return errors.New("Choice identifiers must match their name-derived slug.")
		}
		if c.Terminal && (c.To != "" || c.Session != "") {
			return errors.New("Terminal Choices cannot have a destination or session policy.")
		}
		if c.To != "" {
			if _, ok := g.Nodes[c.To]; !ok {
				return errors.New("Invalid Choice destination.")
			}
		}
		if c.Session != "" {
			target := g.Nodes[c.To]
			if (target.Type != "agent" && target.Type != "join") || (c.Session != "new" && c.Session != "continue_target") {
				return errors.New("Session policy requires an agent or Join destination.")
			}
		}
		for name, f := range c.Input {
			if !fieldNamePattern.MatchString(name) || f.Type != "string" {
				return errors.New("Choice input fields must have normalized names and string type.")
			}
		}
		if err := validateOutput(c.Output, c.Input, false); err != nil {
			return err
		}
	}
	return nil
}
func validateLayout(l GraphLayout) error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) < 1e9 }
	for id, p := range l.Positions {
		if id == "" || !finite(p.X) || !finite(p.Y) {
			return errors.New("Invalid layout position.")
		}
	}
	if v := l.Viewport; v != nil && (!finite(v.X) || !finite(v.Y) || !finite(v.Zoom) || v.Zoom <= 0 || v.Zoom > 10) {
		return errors.New("Invalid viewport.")
	}
	return nil
}
func (a *app) createGraphDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, valid := cleanLabel(body.Name, 80)
	id := authoringSlug(name)
	if !valid || !validAuthoringID(id) {
		fail(w, 400, "Enter a valid graph name.")
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	if _, err := catalogForMutation(p); err != nil {
		fail(w, 409, err.Error())
		return
	}
	if _, err := readConfig(p, graphFile(id)); !errors.Is(err, os.ErrNotExist) {
		fail(w, 409, "A graph with this identifier already exists.")
		return
	}
	// A unique suffix for abandoned draft layouts avoids overwriting another draft.
	draftID := "draft-" + newID()
	l := GraphLayout{Positions: map[string]Point{}}
	b, _ := json.Marshal(l)
	if err := writeConfig(p, layoutFile(draftID), b); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, 201, GraphRecord{draftID, "", GraphDefinition{Name: name, Description: strings.TrimSpace(body.Description), Enabled: true, Nodes: map[string]GraphNode{}, Choices: map[string]GraphChoice{}}, l})
}
func (a *app) saveGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Definition GraphDefinition `json:"definition"`
		Revision   string          `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	oldID := r.PathValue("item")
	if !validAuthoringID(oldID) {
		fail(w, 400, "Invalid graph identifier.")
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	if err = validateGraph(body.Definition, c.Agents); err != nil {
		fail(w, 400, err.Error())
		return
	}
	// Check override compatibility using the same real catalogs as agent registration.
	for _, n := range body.Definition.Nodes {
		if n.Overrides != nil {
			for _, agent := range c.Agents {
				if agent.ID == n.Agent {
					h, m, e := agent.DefaultHarness, agent.Model, agent.Effort
					if n.Overrides.Harness != "" {
						h = n.Overrides.Harness
					}
					if n.Overrides.Model != "" {
						m = n.Overrides.Model
					}
					if n.Overrides.Effort != nil {
						e = *n.Overrides.Effort
					}
					if err = a.validateAgentModel(r, p, h, m, e); err != nil {
						fail(w, 400, err.Error())
						return
					}
				}
			}
		}
	}
	old, readErr := readConfig(p, graphFile(oldID))
	if body.Revision == "" {
		if !errors.Is(readErr, os.ErrNotExist) {
			fail(w, 409, "Graph already exists. Reload before saving.")
			return
		}
	} else if readErr != nil || revision(old) != body.Revision {
		fail(w, 409, "Graph changed. Reload before saving.")
		return
	}
	id := authoringSlug(body.Definition.Name)
	if id != oldID {
		if _, err := readConfig(p, graphFile(id)); !errors.Is(err, os.ErrNotExist) {
			fail(w, 409, "A graph with this identifier already exists.")
			return
		}
	}
	layout, err := readConfig(p, layoutFile(oldID))
	if errors.Is(err, os.ErrNotExist) {
		layout = []byte(`{"positions":{}}`)
	} else if err != nil {
		fail(w, 500, err.Error())
		return
	}
	b, err := yaml.Marshal(body.Definition)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writes := map[string][]byte{graphFile(id): b, layoutFile(id): layout}
	if id != oldID {
		writes[graphFile(oldID)] = nil
		writes[layoutFile(oldID)] = nil
	}
	if err = a.commitGraphAuthoring(p, writes, oldID, id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	l := GraphLayout{}
	_ = json.Unmarshal(layout, &l)
	respond(w, 200, GraphRecord{id, revision(b), body.Definition, l})
}
func (a *app) patchGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled  bool   `json:"enabled"`
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	for _, g := range c.Graphs {
		if g.ID == r.PathValue("item") {
			if g.Revision != body.Revision {
				fail(w, 409, "Graph changed. Reload before updating.")
				return
			}
			g.Definition.Enabled = body.Enabled
			b, _ := yaml.Marshal(g.Definition)
			if err = commitAuthoring(p, map[string][]byte{graphFile(g.ID): b}); err != nil {
				fail(w, 500, err.Error())
				return
			}
			g.Revision = revision(b)
			respond(w, 200, g)
			return
		}
	}
	fail(w, 404, "Graph not found.")
}
func (a *app) deleteGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	for _, g := range c.Graphs {
		if g.ID == r.PathValue("item") {
			if g.Revision != body.Revision {
				fail(w, 409, "Graph changed. Reload before deleting.")
				return
			}
			if err = a.commitGraphAuthoring(p, map[string][]byte{graphFile(g.ID): nil, layoutFile(g.ID): nil}, g.ID, ""); err != nil {
				fail(w, 500, err.Error())
				return
			}
			respond(w, 200, map[string]bool{"removed": true})
			return
		}
	}
	fail(w, 404, "Graph not found.")
}
func (a *app) saveGraphLayout(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	id := r.PathValue("item")
	if !validAuthoringID(id) {
		fail(w, 400, "Invalid graph identifier.")
		return
	}
	var l GraphLayout
	if !decode(w, r, &l) {
		return
	}
	if err := validateLayout(l); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	if err := recoverAuthoring(p); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if _, err := readConfig(p, layoutFile(id)); err != nil {
		if _, ge := readConfig(p, graphFile(id)); ge != nil {
			fail(w, 404, "Graph or draft not found.")
			return
		}
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	if err := writeConfig(p, layoutFile(id), b); err != nil {
		fail(w, 500, err.Error())
		return
	}
	respond(w, 200, l)
}
