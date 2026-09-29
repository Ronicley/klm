package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed pi-permissions.ts
var piPermissionsExtension []byte

func piSamePath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Pi opens a persisted JSONL session independently of the process cwd. Refuse a
// mismatched header instead of silently continuing history in another workspace.
func validatePiSessionDirectory(path, cwd string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("Cannot inspect the stored Pi session directory.")
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if !scanner.Scan() {
		return errors.New("Stored Pi session has no valid header.")
	}
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if json.Unmarshal(scanner.Bytes(), &header) != nil || header.Type != "session" || !filepath.IsAbs(header.Cwd) {
		return errors.New("Stored Pi session has no absolute working directory.")
	}
	actual, err := CanonicalGraphDirectory(header.Cwd)
	if err != nil {
		return err
	}
	wanted, err := CanonicalGraphDirectory(cwd)
	if err != nil {
		return err
	}
	if actual != wanted {
		return errors.New("Stored Pi session belongs to a different working directory.")
	}
	return nil
}

func (p *adapter) runPi(b binary, cwd, text string) (err error) {
	ctx := p.turn.ctx
	if err := ctx.Err(); err != nil {
		return err
	}
	p.app.mu.Lock()
	session := p.app.state.session(p.id)
	if session == nil {
		p.app.mu.Unlock()
		return errors.New("Pi session no longer exists.")
	}
	native, dataDir := p.app.state.Native[p.id], p.app.dir
	p.app.mu.Unlock()
	if !filepath.IsAbs(native.Path) {
		return errors.New("Pi requires an absolute persisted session file path.")
	}
	if native.ID != "" {
		if _, err := os.Stat(native.Path); err != nil {
			return errors.New("Stored Pi session file is unavailable; refusing to silently replace its history.")
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return errors.New("Cannot resolve the Pi working directory.")
	}
	if err := validatePiSessionDirectory(native.Path, cwd); err != nil {
		return err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return errors.New("Cannot resolve the engine data directory.")
	}
	proc, reused := (*interactiveProcess)(nil), false
	toolKey := p.runtimeToolKey()
	if p.runtime != nil {
		proc, reused = p.runtime.getInteractive("pi", p.model, p.effort, toolKey)
	}
	if !reused {
		// The extension directory must live as long as a retained Pi process.
		extensionDir, makeErr := os.MkdirTemp(dataDir, ".pi-permissions-")
		if makeErr != nil {
			return errors.New("Cannot create the Pi permission extension directory.")
		}
		cleanup := func() { _ = os.RemoveAll(extensionDir) }
		extensionPath := filepath.Join(extensionDir, "pi-permissions.ts")
		bridgeConfig, _ := json.Marshal(map[string]any{"url": p.bridge.url, "token": p.bridge.token, "graphNode": p.graphNode(), "tools": p.bridgeTools()})
		extension := strings.Replace(string(piPermissionsExtension), "/*KLM_LINKED_CONFIG*/{}", string(bridgeConfig), 1)
		if writeErr := os.WriteFile(extensionPath, []byte(extension), 0600); writeErr != nil {
			cleanup()
			return errors.New("Cannot write the Pi permission extension.")
		}
		args := []string{"--mode", "rpc", "--no-extensions", "--extension", extensionPath, "--session", native.Path}
		if p.model != "" {
			args = append(args, "--model", p.model)
		}
		if p.effort != "" {
			args = append(args, "--thinking", p.effort)
		}
		proc, err = startInteractive(p.turn, b, args, cwd, p.runtime)
		if err != nil {
			cleanup()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if p.runtime != nil {
			if err := p.runtime.retainInteractive(proc, "pi", p.model, p.effort, toolKey, cleanup); err != nil {
				closeRuntimeInteractive(&runtimeInteractive{process: proc, cleanup: cleanup})
				return err
			}
		} else {
			defer cleanup()
		}
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	startup := timer.C
	p.processesDrained = false
	pending := map[string]string{} // tool call ID -> native UI request ID
	nativeQuestionPending := map[string]string{}
	questionRequests := map[string]*atomic.Bool{}
	defer func() {
		for _, live := range questionRequests {
			live.Store(false)
		}
		if p.runtime == nil {
			_ = proc.Close()
			p.processesDrained = proc.Drained()
		} else {
			reusable := err == nil
			if ctx.Err() != nil && !p.completed {
				reusable = settlePiCancellation(proc)
			}
			if !reusable {
				p.runtime.discardInteractive(proc)
			}
			p.processesDrained = true
		}
		for _, sourceID := range pending {
			if dismissErr := p.dismissPermission(sourceID); err == nil {
				err = dismissErr
			}
		}
		for _, sourceID := range nativeQuestionPending {
			if dismissErr := p.dismissQuestion(sourceID); err == nil {
				err = dismissErr
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	if err := proc.Send(map[string]any{"id": "klm-state", "type": "get_state"}); err != nil {
		return err
	}
	statsSequence, latestStatsID := 0, ""
	statsPrefix := "klm-stats-" + newID() + "-"
	requestStats := func() error {
		statsSequence++
		latestStatsID = statsPrefix + strconv.Itoa(statsSequence)
		return proc.Send(map[string]any{"id": latestStatsID, "type": "get_session_stats"})
	}
	// Optional telemetry gets a bounded final read before releasing the turn.
	statsTimer := time.NewTimer(2 * time.Second)
	statsTimer.Stop()
	defer statsTimer.Stop()
	var statsDeadline <-chan time.Time
	ready, stateReceived, prompted := reused, false, false
	steering := p.steeringChannel()
	steeringRequests := map[string]bool{}
reading:
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !prompted && ready && stateReceived {
			if err := p.bridge.waitReady(ctx); err != nil {
				return err
			}
			p.markRuntimeInfrastructure()
			select {
			case <-startup:
				return errors.New("Pi permission bridge startup timed out.")
			default:
			}
			if err := proc.Send(map[string]any{"id": "klm-prompt", "type": "prompt", "message": text}); err != nil {
				return err
			}
			prompted = true
			timer.Stop()
			startup = nil
		}
		var inputReady <-chan struct{}
		if prompted && !p.completed {
			inputReady = steering
		}
		var raw map[string]any
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-inputReady:
			q, payload, err := p.takeSteering()
			if err != nil {
				return err
			}
			if q != nil {
				steeringRequests["klm-steer-"+q.ID] = true
				if err := proc.Send(map[string]any{"id": "klm-steer-" + q.ID, "type": "steer", "message": payload.piText()}); err != nil {
					return err
				}
			}
			continue
		case <-statsDeadline:
			return nil
		case <-startup:
			return errors.New("Pi permission bridge startup timed out.")
		case raw, ok = <-proc.Frames:
		case <-proc.Done:
			// Preserve terminal events already buffered when the child exits.
			select {
			case raw, ok = <-proc.Frames:
			default:
				break reading
			}
		}
		if !ok {
			break
		}
		switch str(raw, "type") {
		case "queue_update":
			continue
		case "response":
			if id := str(raw, "id"); strings.HasPrefix(id, "klm-steer-") {
				if !steeringRequests[id] {
					continue
				}
				delete(steeringRequests, id)
				failure := ""
				if !truth(raw, "success") {
					failure = humanError(raw["error"])
					if failure == "" {
						failure = "Pi did not accept this message. Send it again when ready."
					}
				}
				if err := p.finishSteering(strings.TrimPrefix(id, "klm-steer-"), failure); err != nil {
					return err
				}
				continue
			}
			if strings.HasPrefix(str(raw, "id"), "klm-stats-") {
				if str(raw, "id") != latestStatsID {
					continue
				}
				data := object(raw["data"])
				if truth(raw, "success") && str(raw, "command") == "get_session_stats" &&
					piSamePath(str(data, "sessionFile"), native.Path) {
					if err := p.setUsage(piSessionUsage(data)); err != nil {
						return err
					}
				}
				if p.completed {
					return nil
				}
				continue
			}
			if !truth(raw, "success") {
				if err := p.failure(raw); err != nil {
					return err
				}
				return errors.New("Pi RPC command failed.")
			}
			switch str(raw, "id") {
			case "klm-state":
				data := object(raw["data"])
				if stateReceived || str(raw, "command") != "get_state" || str(data, "sessionId") == "" ||
					!piSamePath(str(data, "sessionFile"), native.Path) || (native.ID != "" && str(data, "sessionId") != native.ID) {
					return errors.New("Pi returned an invalid or unexpected session state.")
				}
				if err := p.nativeID(str(data, "sessionId")); err != nil {
					return err
				}
				stateReceived = true
				model := object(data["model"])
				if err := p.resolvedSelection(str(model, "provider")+"/"+str(model, "id"), str(data, "thinkingLevel")); err != nil {
					return err
				}
				_ = requestStats()
			case "klm-prompt":
				if !prompted || str(raw, "command") != "prompt" {
					return errors.New("Pi returned an unexpected prompt response.")
				}
			}
			// RPC acknowledgements are not streaming events or turn completion.
		case "extension_ui_request":
			method, sourceID := str(raw, "method"), str(raw, "id")
			if method == "notify" {
				if str(raw, "notifyType") == "error" {
					if err := p.failure(raw); err != nil {
						return err
					}
					return errors.New("Pi extension reported an error.")
				}
				if str(raw, "message") == "klm.permissions.ready.v1" && str(raw, "notifyType") == "info" {
					ready = true
				}
				continue
			}
			if method != "select" && method != "confirm" && method != "input" && method != "editor" {
				continue // Nonblocking UI decoration has no engine counterpart.
			}
			title := str(raw, "title")
			const questionPrefix = "klm.question.v1:"
			if strings.HasPrefix(title, questionPrefix) {
				var request struct {
					ToolCallID string `json:"toolCallId"`
					Cwd        string `json:"cwd"`
					Questions  []struct {
						Question string           `json:"question"`
						Header   string           `json:"header"`
						Options  []QuestionOption `json:"options"`
						Multiple bool             `json:"multiple"`
						Custom   *bool            `json:"custom"`
					} `json:"questions"`
				}
				decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(title, questionPrefix)))
				decoder.DisallowUnknownFields()
				decodeErr := decoder.Decode(&request)
				valid := prompted && method == "input" && strings.TrimSpace(sourceID) != "" &&
					len(title) <= 262144+len(questionPrefix) && decodeErr == nil && decoder.Decode(new(any)) == io.EOF &&
					strings.TrimSpace(request.ToolCallID) != "" && len(request.ToolCallID) <= 256 &&
					piSamePath(request.Cwd, cwd) && len(request.Questions) > 0 && len(request.Questions) <= 12 &&
					nativeQuestionPending[request.ToolCallID] == "" && pending[request.ToolCallID] == "" && questionRequests[sourceID] == nil
				for _, id := range pending {
					if id == sourceID {
						valid = false
					}
				}
				if !valid {
					if sourceID != "" {
						if err := proc.Send(map[string]any{"type": "extension_ui_response", "id": sourceID, "cancelled": true}); err != nil {
							return err
						}
					}
					return errors.New("Pi requested an invalid question dialog; execution was stopped.")
				}
				req := QuestionRequest{SourceID: sourceID}
				for index, question := range request.Questions {
					req.Items = append(req.Items, QuestionItem{
						ID: strconv.Itoa(index), Header: question.Header, Text: question.Question,
						Options: question.Options, Multiple: question.Multiple,
						Custom: question.Custom == nil || *question.Custom,
					})
				}
				live := &atomic.Bool{}
				live.Store(true)
				nativeQuestionPending[request.ToolCallID] = sourceID
				questionRequests[sourceID] = live
				if err := p.requestQuestion(req, func(answers [][]string, cancelled bool) error {
					if ctx.Err() != nil || !live.CompareAndSwap(true, false) {
						return errors.New("The Pi question is no longer active.")
					}
					response := map[string]any{"type": "extension_ui_response", "id": sourceID}
					if cancelled {
						response["cancelled"] = true
					} else {
						encoded, err := json.Marshal(answers)
						if err != nil {
							return err
						}
						response["value"] = string(encoded)
					}
					return proc.Send(response)
				}); err != nil {
					return err
				}
				continue
			}
			const prefix = "klm.permission.v1:"
			var request struct {
				ToolCallID string         `json:"toolCallId"`
				ToolName   string         `json:"toolName"`
				Cwd        string         `json:"cwd"`
				Input      map[string]any `json:"input"`
			}
			decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(title, prefix)))
			decoder.UseNumber()
			decoder.DisallowUnknownFields()
			decodeErr := decoder.Decode(&request)
			options, _ := raw["options"].([]any)
			valid := prompted && method == "select" && strings.TrimSpace(sourceID) != "" &&
				strings.HasPrefix(title, prefix) && decodeErr == nil && decoder.Decode(new(any)) == io.EOF &&
				strings.TrimSpace(request.ToolCallID) != "" && strings.TrimSpace(request.ToolName) != "" &&
				request.Input != nil && piSamePath(request.Cwd, cwd) && pending[request.ToolCallID] == "" &&
				nativeQuestionPending[request.ToolCallID] == "" && questionRequests[sourceID] == nil &&
				len(options) == 2 && options[0] == "Allow" && options[1] == "Deny"
			for _, id := range pending {
				if id == sourceID {
					valid = false
				}
			}
			if !valid {
				if sourceID != "" {
					if err := proc.Send(map[string]any{"type": "extension_ui_response", "id": sourceID, "cancelled": true}); err != nil {
						return err
					}
				}
				return errors.New("Pi requested an invalid or unsupported permission dialog; execution was stopped.")
			}
			scope := map[string]any{"toolName": request.ToolName, "cwd": request.Cwd, "input": request.Input}
			arguments, err := json.Marshal(request.Input)
			if err != nil {
				return errors.New("Cannot encode Pi permission arguments.")
			}
			pattern := str(request.Input, "command")
			if pattern == "" {
				pattern = str(request.Input, "path")
			}
			if pattern == "" {
				pattern = string(arguments)
			}
			pending[request.ToolCallID] = sourceID
			if err := p.requestPermission(Permission{
				Harness: "pi", Kind: "tool", Title: "Allow " + request.ToolName + "?",
				Description: pattern, Patterns: []string{pattern},
				Details: map[string]any{"toolCallId": request.ToolCallID, "toolName": request.ToolName, "cwd": request.Cwd, "input": request.Input},
				Command: str(request.Input, "command"), Path: request.Cwd,
				Decisions: []string{"once", "session", "always", "reject"}, AllowLabel: "Allow", SourceID: sourceID,
			}, scope, func(allow bool) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				value := "Deny"
				if allow && !p.graphSealed() {
					value = "Allow"
				}
				return proc.Send(map[string]any{"type": "extension_ui_response", "id": sourceID, "value": value})
			}); err != nil {
				return err
			}
		case "extension_error", "error":
			if err := p.failure(raw); err != nil {
				return err
			}
			return errors.New("Pi reported a fatal error.")
		default:
			if !prompted {
				continue
			}
			if str(raw, "type") == "auto_compaction_start" {
				if err := p.clearContextUsage(); err != nil {
					return err
				}
			}
			if str(raw, "type") == "tool_execution_end" {
				toolID := str(raw, "toolCallId")
				if str(raw, "toolName") == "graph_submit_choice" && !truth(raw, "isError") {
					p.graphChoiceToolSettled(raw["result"])
				}
				if sourceID := nativeQuestionPending[toolID]; sourceID != "" {
					questionRequests[sourceID].Store(false)
					if err := p.dismissQuestion(sourceID); err != nil {
						return err
					}
					delete(nativeQuestionPending, toolID)
				}
				if sourceID := pending[toolID]; sourceID != "" {
					if err := p.dismissPermission(sourceID); err != nil {
						return err
					}
					delete(pending, toolID)
				}
			}
			if err := p.pi(raw); err != nil {
				return err
			}
			if str(raw, "type") == "agent_settled" {
				p.nativeSettled = true
				// Usage is optional. A correlated stats response received before
				// settlement has already been applied; do not delay idle for it.
				if p.completed {
					return nil
				}
			}
			if p.completed {
				if statsDeadline == nil {
					if requestStats() != nil {
						return nil
					}
					statsTimer.Reset(2 * time.Second)
					statsDeadline = statsTimer.C
				}
			} else if (str(raw, "type") == "message_end" && str(object(raw["message"]), "role") == "assistant") || str(raw, "type") == "auto_compaction_end" {
				_ = requestStats()
			}
		}
	}
	if p.completed {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-startup:
		return errors.New("Pi permission bridge startup timed out.")
	case <-proc.Done:
	}
	if err := proc.Err(); err != nil {
		return err
	}
	if !prompted {
		return errors.New("Pi exited before the permission bridge and session state were ready.")
	}
	return errors.New("Pi RPC stream ended before agent_settled.")
}

func settlePiCancellation(proc *interactiveProcess) bool {
	if proc.Send(map[string]any{"id": "klm-abort", "type": "abort"}) != nil {
		return false
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return false
		case <-proc.Done:
			return false
		case frame, ok := <-proc.Frames:
			if !ok {
				return false
			}
			if str(frame, "type") == "agent_settled" {
				return true
			}
			if str(frame, "type") == "response" && str(frame, "id") == "klm-abort" && !truth(frame, "success") {
				return false
			}
		}
	}
}
