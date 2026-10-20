package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

func openCodeMCPToolPrefixes(servers map[string]any) []string {
	var prefixes []string
	for name := range servers {
		if name == "" {
			continue
		}
		// Native McpCatalog.toolName: sanitized server name + '_' + tool name.
		name = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, name)
		prefixes = append(prefixes, name+"_")
	}
	return prefixes
}

func (p *adapter) isOpenCodeMCPTool(tool string) bool {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	for _, prefix := range p.openCodeMCPPrefixes {
		if strings.HasPrefix(tool, prefix) && len(tool) > len(prefix) {
			return true
		}
	}
	return false
}

func (p *adapter) openCodePermissionGate(ctx context.Context, raw json.RawMessage) (bool, error) {
	var call struct {
		SessionID string         `json:"sessionID"`
		CallID    string         `json:"toolCallId"`
		Tool      string         `json:"toolName"`
		Cwd       string         `json:"cwd"`
		Input     map[string]any `json:"input"`
	}
	if json.Unmarshal(raw, &call) != nil || call.SessionID == "" || call.CallID == "" || call.Tool == "" || call.Input == nil || !piSamePath(call.Cwd, p.cwd) {
		return false, errors.New("Invalid OpenCode tool preflight.")
	}
	if err := p.turn.ctx.Err(); err != nil {
		return false, err
	}
	if p.graphSealed() {
		return false, errors.New("Graph activation is sealed.")
	}
	if p.capabilities != nil && strings.HasPrefix(call.Tool, "klm_linked_") {
		p.app.mu.Lock()
		root := p.app.state.Native[p.id].ID
		p.app.mu.Unlock()
		if call.SessionID != root {
			return false, errors.New("Conversation-role tools are not delegated to native subagents.")
		}
	}
	if p.capabilities != nil && call.Tool == "skill" {
		allowed := false
		p.app.mu.Lock()
		native := p.app.state.Native[p.id].ID
		p.app.mu.Unlock()
		for _, skill := range p.capabilities.Skills {
			if skill.Name == str(call.Input, "name") && (!skill.Internal || call.SessionID == native) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, errors.New("Skill is not selected for this conversation role.")
		}
	}
	if p.internalOpenCodeTool(call.Tool) || slices.Contains([]string{"question", "todowrite", "todoread"}, call.Tool) {
		return true, nil
	}
	key := call.SessionID + "/" + call.CallID
	result := make(chan bool, 1)
	encoded, _ := json.Marshal(call.Input)
	req := Permission{SourceID: "preflight:" + key, Kind: call.Tool, Title: "Allow " + call.Tool,
		Patterns: []string{string(encoded)}, Command: str(call.Input, "command"), Path: call.Cwd,
		Details: map[string]any{"toolName": call.Tool, "input": call.Input, "cwd": call.Cwd}}
	req.mcpTool = p.isOpenCodeMCPTool(call.Tool)
	if call.Tool == "bash" {
		if workdir := str(call.Input, "workdir"); workdir != "" {
			req.Path = workdir
		}
	}
	if err := p.requestPermission(req, map[string]any{"tool": call.Tool, "input": call.Input, "cwd": call.Cwd}, func(allow bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.turn.ctx.Err(); err != nil {
			return err
		}
		if p.graphSealed() {
			allow = false
		}
		select {
		case result <- allow:
			return nil
		default:
			return errors.New("Permission already resolved.")
		}
	}); err != nil {
		return false, err
	}
	defer p.dismissPermission(req.SourceID)
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-p.turn.ctx.Done():
		return false, p.turn.ctx.Err()
	case allow := <-result:
		if allow {
			p.app.mu.Lock()
			if p.gatedPermissions == nil {
				p.gatedPermissions = map[string]string{}
			}
			p.gatedPermissions[key] = call.Tool
			p.app.mu.Unlock()
		}
		return allow, nil
	}
}

// The native prompt is for a call already checked with its full arguments. Do
// not broaden that approval to unrelated guards such as doom_loop.
func (p *adapter) openCodePermissionApproved(properties map[string]any) bool {
	key := str(properties, "sessionID") + "/" + str(object(properties["tool"]), "callID")
	p.app.mu.Lock()
	tool := p.gatedPermissions[key]
	p.app.mu.Unlock()
	kind := str(properties, "permission")
	if tool == "" {
		return false
	}
	return kind == tool || kind == "external_directory" && slices.Contains([]string{"read", "write", "edit", "apply_patch", "bash", "glob", "grep", "list", "skill"}, tool) ||
		kind == "edit" && slices.Contains([]string{"write", "edit", "apply_patch"}, tool)
}
