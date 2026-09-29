package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type codexInteractive struct {
	p        *adapter
	send     func(any) error
	threadID string
	turnID   string
	cwd      string
	items    map[string]map[string]any
	parts    map[string]map[string]bool
	requests map[string]*atomic.Bool
	children map[string]*codexSubagent
}

type codexSubagent struct {
	adapter   *adapter
	parentKey string
	turnID    string
	items     map[string]map[string]any
	parts     map[string]map[string]bool
}

func (p *adapter) runCodex(b binary, cwd string, payload submission) (err error) {
	p.app.mu.Lock()
	native := p.app.state.Native[p.id]
	sessionModel := p.model
	p.app.mu.Unlock()

	process, reused := (*interactiveProcess)(nil), false
	toolKey := p.runtimeToolKey()
	if p.runtime != nil {
		process, reused = p.runtime.getInteractive("codex", "", "", toolKey)
	}
	if !reused {
		process, err = startInteractive(p.turn, b, []string{
			"-c", "features.default_mode_request_user_input=true",
			"-c", "tools.experimental_request_user_input.enabled=true",
			"-c", "mcp_servers.klm_linked.url=" + strconv.Quote(p.bridge.url),
			"-c", "mcp_servers.klm_linked.http_headers={Authorization=" + strconv.Quote("Bearer "+p.bridge.token) + "}",
			"-c", "mcp_servers.klm_linked.enabled=true",
			"-c", "mcp_servers.klm_linked.startup_timeout_sec=15",
			"-c", "mcp_servers.klm_linked.tool_timeout_sec=12",
			"app-server", "--listen", "stdio://",
		}, cwd, p.runtime)
		if err != nil {
			return err
		}
		if p.runtime != nil {
			if err := p.runtime.retainInteractive(process, "codex", "", "", toolKey, nil); err != nil {
				closeRuntimeInteractive(&runtimeInteractive{process: process})
				return err
			}
		}
	}
	p.processesDrained = false
	cancelSettled := false
	defer func() {
		if p.runtime == nil {
			_ = process.Close()
			for frame := range process.Frames {
				p.observeCodexMCP(str(frame, "method"), object(frame["params"]))
			}
			p.processesDrained = process.Drained() && !p.processDrainFailed
		} else {
			if err != nil && !(p.turn.ctx.Err() != nil && cancelSettled) {
				p.runtime.discardInteractive(process)
			}
			p.processesDrained = true
		}
	}()
	c := &codexInteractive{p: p, cwd: cwd, items: map[string]map[string]any{},
		parts: map[string]map[string]bool{}, requests: map[string]*atomic.Bool{}, children: map[string]*codexSubagent{}}
	defer func() {
		for _, live := range c.requests {
			live.Store(false)
		}
	}()
	// A stalled stdin must not prevent startup deadlines or cancellation.
	c.send = func(value any) error {
		result := make(chan error, 1)
		go func() { result <- process.Send(value) }()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-result:
			return err
		case <-p.turn.ctx.Done():
			return p.turn.ctx.Err()
		case <-timer.C:
			return errors.New("Codex app-server stdin timed out.")
		}
	}
	// Give a cancelled native turn a bounded chance to settle before deciding
	// whether its persistent app-server can be reused.
	defer func() {
		if p.turn.ctx.Err() == nil || c.threadID == "" || c.turnID == "" || p.completed {
			return
		}
		sent := make(chan error, 1)
		go func() {
			sent <- process.Send(map[string]any{"id": "klm-interrupt", "method": "turn/interrupt",
				"params": map[string]any{"threadId": c.threadID, "turnId": c.turnID}})
		}()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				return
			case <-process.Done:
				return
			case err := <-sent:
				if err != nil {
					return
				}
				sent = nil
			case frame, ok := <-process.Frames:
				if !ok {
					return
				}
				params := object(frame["params"])
				p.observeCodexMCP(str(frame, "method"), params)
				if str(frame, "method") == "turn/completed" && str(params, "threadId") == c.threadID &&
					str(object(params["turn"]), "id") == c.turnID {
					cancelSettled = true
					return
				}
			}
		}
	}()

	if !reused {
		if err := c.send(map[string]any{"id": "klm-init", "method": "initialize", "params": map[string]any{
			"clientInfo":   map[string]any{"name": "klm", "version": "0.1.0"},
			"capabilities": map[string]any{"experimentalApi": true, "mcpServerOpenaiFormElicitation": false, "requestAttestation": false},
		}}); err != nil {
			return err
		}
	}
	phase := "klm-init"
	if reused {
		phase = "klm-thread"
	}
	mcpBridgeFound := false
	startup := time.NewTimer(30 * time.Second)
	defer startup.Stop()
	deadline := startup.C
	var early []map[string]any
	frames, done := process.Frames, process.Done
	choiceStop := p.graphStop()
	choiceInterrupted := false
	steering := p.steeringChannel()
	steeringRequests := map[string]bool{}
	approvalPolicy, sandbox, sandboxType := "on-request", "read-only", "readOnly"
	if p.yolo {
		approvalPolicy, sandbox, sandboxType = "never", "danger-full-access", "dangerFullAccess"
	}
	sendThread := func() error {
		params := map[string]any{"cwd": cwd, "approvalPolicy": approvalPolicy, "approvalsReviewer": "user", "sandbox": sandbox}
		method := "thread/start"
		if native.ID != "" {
			method, params["threadId"] = "thread/resume", native.ID
		}
		if sessionModel != "" {
			params["model"] = sessionModel
		}
		return c.send(map[string]any{"id": "klm-thread", "method": method, "params": params})
	}
	if reused {
		if err := sendThread(); err != nil {
			return err
		}
	}
	for {
		if !choiceInterrupted && p.choiceToolSettled && !p.graphMCPPending() {
			choiceStop = p.graphStop()
		}
		var inputReady <-chan struct{}
		if phase == "" && c.turnID != "" && !p.completed {
			inputReady = steering
		}
		var frame map[string]any
		var ok bool
		// Give pending steering a dispatch opportunity even when the frame
		// channel is continuously nonempty. Native acknowledgement is separate.
		if inputReady != nil {
			select {
			case <-inputReady:
				q, payload, err := p.takeSteering()
				if err != nil {
					return err
				}
				if q != nil {
					steeringRequests["klm-steer-"+q.ID] = true
					if err := c.send(map[string]any{"id": "klm-steer-" + q.ID, "method": "turn/steer", "params": map[string]any{"threadId": c.threadID, "expectedTurnId": c.turnID, "input": payload.codexInput()}}); err != nil {
						return err
					}
				}
				continue
			default:
			}
		}
		// Drain buffered output before interpreting process exit as a missing
		// terminal turn. A completed turn does not require app-server to exit.
		select {
		case frame, ok = <-frames:
		default:
			if done == nil {
				if err := process.Err(); err != nil {
					return err
				}
				return errors.New("Codex app-server exited without completing the turn.")
			}
			select {
			case <-inputReady:
				q, payload, err := p.takeSteering()
				if err != nil {
					return err
				}
				if q != nil {
					steeringRequests["klm-steer-"+q.ID] = true
					if err := c.send(map[string]any{"id": "klm-steer-" + q.ID, "method": "turn/steer", "params": map[string]any{"threadId": c.threadID, "expectedTurnId": c.turnID, "input": payload.codexInput()}}); err != nil {
						return err
					}
				}
				continue
			case <-choiceStop:
				choiceStop = nil
				if p.graphMCPPending() {
					continue
				}
				if p.completed {
					return p.finishCodexNative(process, b, cwd, c.threadID, c.turnID)
				}
				choiceInterrupted = true
				if c.threadID == "" || c.turnID == "" {
					return errors.New("Choice completed before the native Codex turn was identified.")
				}
				if err := c.send(map[string]any{"id": "klm-choice-interrupt", "method": "turn/interrupt", "params": map[string]any{"threadId": c.threadID, "turnId": c.turnID}}); err != nil {
					return err
				}
				continue
			case frame, ok = <-frames:
			case <-done:
				done = nil
				continue
			case <-p.turn.ctx.Done():
				return p.turn.ctx.Err()
			case <-deadline:
				return errors.New("Codex app-server startup timed out.")
			}
		}
		if !ok {
			frames = nil
			continue
		}
		if err := p.turn.ctx.Err(); err != nil {
			return err
		}
		if deadline != nil {
			select {
			case <-deadline:
				return errors.New("Codex app-server startup timed out.")
			default:
			}
		}
		method := str(frame, "method")
		if method != "" {
			if phase == "klm-turn" {
				// Notifications and requests can precede turn/start's response.
				// Do not offer consent until its native turn ID is confirmed.
				early = append(early, frame)
				continue
			}
			if err := c.frame(frame); err != nil {
				return err
			}
			if p.completed && !p.graphMCPPending() {
				return p.finishCodexNative(process, b, cwd, c.threadID, c.turnID)
			}
			continue
		}
		id, isString := frame["id"].(string)
		if isString && strings.HasPrefix(id, "klm-steer-") {
			if !steeringRequests[id] {
				continue
			}
			delete(steeringRequests, id)
			failure := ""
			if frame["error"] != nil {
				failure = humanError(frame["error"])
				if failure == "" {
					failure = "Codex did not accept this message. Send it again when ready."
				}
			} else if str(object(frame["result"]), "turnId") != c.turnID {
				return errors.New("Codex steering acknowledgement identified a different turn.")
			}
			if err := p.finishSteering(strings.TrimPrefix(id, "klm-steer-"), failure); err != nil {
				return err
			}
			continue
		}
		if !isString || phase == "" || id != phase {
			continue
		}
		if frame["error"] != nil {
			message := humanError(frame["error"])
			if message == "" {
				message = "Codex app-server rejected " + phase + "."
			}
			return errors.New(message)
		}
		result := object(frame["result"])
		if result == nil {
			return errors.New("Codex app-server returned an invalid RPC result.")
		}
		switch phase {
		case "klm-init":
			if err := c.send(map[string]any{"method": "initialized"}); err != nil {
				return err
			}
			phase = "klm-thread"
			if err := sendThread(); err != nil {
				return err
			}
		case "klm-thread":
			threadID := str(object(result["thread"]), "id")
			returnedCwd := str(result, "cwd")
			sameCwd := returnedCwd != "" && filepath.Clean(returnedCwd) == filepath.Clean(cwd)
			if runtime.GOOS == "windows" && returnedCwd != "" {
				sameCwd = strings.EqualFold(filepath.Clean(returnedCwd), filepath.Clean(cwd))
			}
			if threadID == "" || (native.ID != "" && threadID != native.ID) || !sameCwd {
				return errors.New("Codex returned a different thread or working directory; refusing to start a turn.")
			}
			if str(result, "approvalPolicy") != approvalPolicy || str(object(result["sandbox"]), "type") != sandboxType {
				return errors.New("Codex did not confirm the requested approval policy and sandbox.")
			}
			if reviewer := str(result, "approvalsReviewer"); reviewer != "" && reviewer != "user" {
				return errors.New("Codex did not retain user-reviewed approvals.")
			}
			if err := p.nativeID(threadID); err != nil {
				return err
			}
			c.threadID = threadID
			p.setGraphNativeSession(threadID)
			effort := p.effort
			if effort == "" {
				effort = str(result, "reasoningEffort")
			}
			if err := p.resolvedSelection(str(result, "model"), effort); err != nil {
				return err
			}
			phase = "klm-mcp"
			if err := c.send(map[string]any{"id": phase, "method": "mcpServerStatus/list", "params": map[string]any{"threadId": threadID, "limit": 100}}); err != nil {
				return err
			}
		case "klm-mcp":
			found := false
			servers, _ := result["data"].([]any)
			for _, value := range servers {
				server := object(value)
				if str(server, "name") != "klm_linked" {
					continue
				}
				tools := object(server["tools"])
				found = true
				for _, tool := range p.bridgeTools() {
					exists := false
					for key, value := range tools {
						if key == str(tool, "name") || str(object(value), "name") == str(tool, "name") {
							exists = true
						}
					}
					found = found && exists
				}
			}
			mcpBridgeFound = mcpBridgeFound || found
			if (p.graphNode() || !mcpBridgeFound) && str(result, "nextCursor") != "" {
				if err := c.send(map[string]any{"id": phase, "method": "mcpServerStatus/list", "params": map[string]any{"threadId": c.threadID, "limit": 100, "cursor": result["nextCursor"]}}); err != nil {
					return err
				}
				continue
			}
			if !mcpBridgeFound {
				return errors.New("Codex did not expose the linked-agent tools.")
			}
			if err := p.bridge.waitReady(p.turn.ctx); err != nil {
				return err
			}
			p.markRuntimeInfrastructure()
			phase = "klm-turn"
			turnParams := map[string]any{
				"threadId": c.threadID, "input": payload.codexInput(),
			}
			if p.effort != "" {
				turnParams["effort"] = p.effort
			}
			if err := c.send(map[string]any{"id": phase, "method": "turn/start", "params": turnParams}); err != nil {
				return err
			}
		case "klm-turn":
			c.turnID = str(object(result["turn"]), "id")
			if !codexIdentifier(c.turnID) {
				return errors.New("Codex returned an invalid native turn identifier.")
			}
			phase, deadline = "", nil
			startup.Stop()
			for _, queued := range early {
				if err := c.frame(queued); err != nil {
					return err
				}
				if p.completed && !p.graphMCPPending() {
					return p.finishCodexNative(process, b, cwd, c.threadID, c.turnID)
				}
			}
			early = nil
		}
	}
}

// turn/completed is emitted before Codex's final transcript flush. For graph
// sessions, request EOF shutdown, then use a fresh read-only app-server connection
// to read persisted history WITHOUT resuming/starting a thread or sending a prompt.
// This checks the actual stored tool-result pair before claiming continuability.
func (p *adapter) finishCodexNative(process *interactiveProcess, b binary, cwd, threadID, turnID string) error {
	if !p.graphNode() {
		return nil
	}
	if err := finishCodexProcess(process, p.turn.ctx, func(frame map[string]any) { p.observeCodexMCP(str(frame, "method"), object(frame["params"])) }); err != nil {
		return err
	}
	if !process.Drained() {
		return errors.New("Codex owned processes did not drain after native shutdown.")
	}
	probe, err := startInteractive(p.turn, b, []string{"app-server", "--listen", "stdio://"}, cwd, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = probe.Close()
		p.processDrainFailed = p.processDrainFailed || !probe.Drained()
	}()
	if err := probe.Send(map[string]any{"id": "klm-verify-init", "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "klm", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}}}); err != nil {
		return err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	phase := "klm-verify-init"
	readingThread := threadID
	readThreads := map[string]bool{}
	for {
		select {
		case <-p.turn.ctx.Done():
			return p.turn.ctx.Err()
		case <-timer.C:
			return errors.New("Codex persisted-history verification timed out.")
		case frame, ok := <-probe.Frames:
			if !ok {
				return errors.New("Codex closed before persisted-history verification.")
			}
			if str(frame, "id") != phase {
				continue
			}
			if frame["error"] != nil {
				return errors.New("Codex could not verify the persisted graph thread.")
			}
			if phase == "klm-verify-init" {
				if err := probe.Send(map[string]any{"method": "initialized"}); err != nil {
					return err
				}
				phase = "klm-verify-history"
				if err := probe.Send(map[string]any{"id": phase, "method": "thread/read", "params": map[string]any{"threadId": threadID, "includeTurns": true}}); err != nil {
					return err
				}
				continue
			}
			thread := object(object(frame["result"])["thread"])
			if str(thread, "id") != readingThread {
				return errors.New("Codex persisted-history response belongs to a different thread.")
			}
			turns, _ := thread["turns"].([]any)
			matched, receipt := false, false
			for _, value := range turns {
				nativeTurn := object(value)
				if readingThread != threadID {
					p.auditCodexChildTurn(readingThread, nativeTurn)
					continue
				}
				if str(nativeTurn, "status") == "inProgress" {
					return errors.New("Codex persisted graph history still has an unfinished turn.")
				}
				if str(nativeTurn, "id") != turnID {
					continue
				}
				status := str(nativeTurn, "status")
				matched = status == "completed" || status == "interrupted"
				items, _ := nativeTurn["items"].([]any)
				for _, value := range items {
					item := object(value)
					p.observeCodexMCP("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item})
					if str(item, "type") == "mcpToolCall" && str(item, "server") == "klm_linked" && str(item, "tool") == "graph_submit_choice" {
						receipt = receipt || p.graphReservationReceipt(item["result"])
					}
				}
			}
			if readingThread == threadID && (!matched || (p.graphSealed() && !receipt)) {
				return errors.New("Codex persisted history does not contain the settled graph turn and its reserved Choice result.")
			}
			readThreads[readingThread] = true
			if readingThread != threadID {
				p.graph.mu.Lock()
				if p.graph.codexAudited == nil {
					p.graph.codexAudited = map[string]bool{}
				}
				p.graph.codexAudited[readingThread] = true
				p.graph.mu.Unlock()
			}
			next := ""
			for _, id := range p.codexChildIDs() {
				if !readThreads[id] {
					next = id
					break
				}
			}
			if next != "" {
				readingThread = next
				if err := probe.Send(map[string]any{"id": phase, "method": "thread/read", "params": map[string]any{"threadId": next, "includeTurns": true}}); err != nil {
					return err
				}
				continue
			}
			if err := finishCodexProcess(probe, p.turn.ctx, nil); err != nil {
				return err
			}
			if !probe.Drained() {
				return errors.New("Codex history-verification process drainage is unconfirmed.")
			}
			return nil
		}
	}
}

func finishCodexProcess(process *interactiveProcess, ctx context.Context, observe func(map[string]any)) error {
	_ = process.EndInput()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	frames := process.Frames
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("Codex native shutdown was not confirmed.")
		case frame, ok := <-frames:
			if ok && observe != nil {
				observe(frame)
			}
			if !ok {
				frames = nil
			}
		case <-process.Done:
			for frame := range process.Frames {
				if observe != nil {
					observe(frame)
				}
			}
			err := process.Err()
			closeErr := process.Close()
			return errors.Join(err, closeErr)
		}
	}
}

func codexIdentifier(id string) bool {
	return id != "" && len(id) <= 256 && strings.IndexFunc(id, func(r rune) bool {
		return r <= ' ' || r == 127
	}) == -1
}

func codexRPCID(id any) (string, bool) {
	switch value := id.(type) {
	case string:
		if !codexIdentifier(value) {
			return "", false
		}
	case json.Number:
		if strings.IndexFunc(string(value), func(r rune) bool { return (r < '0' || r > '9') && r != '-' }) != -1 {
			return "", false
		}
	default:
		return "", false
	}
	encoded, err := json.Marshal(id)
	return string(encoded), err == nil
}

func codexJSON(value any) string {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return string(encoded)
}

func (c *codexInteractive) warning(text string) error {
	return c.p.put("", "status", "Codex warning", text, "warning", false, nil)
}

func (c *codexInteractive) frame(frame map[string]any) error {
	method, params := str(frame, "method"), object(frame["params"])
	c.p.observeCodexMCP(method, params)
	if _, request := frame["id"]; request {
		return c.request(frame)
	}
	if method == "serverRequest/resolved" {
		if c.threadID != "" && str(params, "threadId") == c.threadID {
			if sourceID, valid := codexRPCID(params["requestId"]); valid {
				if live := c.requests[sourceID]; live != nil {
					live.Store(false)
					permissionErr := c.p.dismissPermission(sourceID)
					questionErr := c.p.dismissQuestion(sourceID)
					return errors.Join(permissionErr, questionErr)
				}
			}
		}
		return nil
	}
	if child := c.children[str(params, "threadId")]; child != nil {
		return c.childFrame(child, method, params)
	}
	if c.turnID == "" || str(params, "threadId") != c.threadID {
		return nil
	}
	if method == "turn/completed" {
		nativeTurn := object(params["turn"])
		if str(nativeTurn, "id") != c.turnID {
			return nil
		}
		status, message := "completed", "Turn completed."
		switch str(nativeTurn, "status") {
		case "failed":
			c.p.completed = true
			return c.p.failure(map[string]any{"type": method, "message": nativeTurn["error"]})
		case "interrupted":
			if !c.p.graphSealed() {
				return errors.New("Codex turn was interrupted.")
			}
			status, message = "cancelled", "Turn interrupted."
		case "completed":
		default:
			return errors.New("Codex returned an unknown terminal turn status.")
		}
		c.p.completed = true
		c.p.nativeSettled = true
		return c.p.put("", "status", "", message, status, false, nil)
	}
	if str(params, "turnId") != c.turnID {
		return nil
	}
	if method == "thread/tokenUsage/updated" {
		return c.p.setUsage(codexSessionUsage(object(params["tokenUsage"])))
	}
	if method == "error" {
		message := humanError(params["error"])
		if message == "" {
			message = "Codex reported a nonterminal error."
		}
		if truth(params, "willRetry") {
			message += " Codex will retry."
		}
		return c.warning(message)
	}
	if method == "item/started" || method == "item/completed" {
		item := object(params["item"])
		id := str(item, "id")
		if !codexIdentifier(id) {
			return errors.New("Codex returned an item without a valid identifier.")
		}
		c.items[id] = item
		if method == "item/completed" && str(item, "type") == "mcpToolCall" && str(item, "server") == "klm_linked" && str(item, "tool") == "graph_submit_choice" && item["error"] == nil && str(item, "status") != "failed" {
			c.p.graphChoiceToolSettled(item["result"])
		}
		if str(item, "type") == "contextCompaction" {
			if err := c.p.clearContextUsage(); err != nil {
				return err
			}
		}
		return c.item(item, method == "item/completed")
	}
	id := str(params, "itemId")
	if !codexIdentifier(id) {
		return nil
	}
	key, kind, title := "codex/item/"+id, "", ""
	switch method {
	case "item/agentMessage/delta":
		kind = "assistant"
	case "item/commandExecution/outputDelta":
		kind, title = "command", "Command"
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		kind = "reasoning"
		field, group := "summaryIndex", "summary"
		if method == "item/reasoning/textDelta" {
			field, group = "contentIndex", "content"
		}
		index, valid := params[field].(json.Number)
		n, err := index.Int64()
		if !valid || err != nil || n < 0 || n > 10000 {
			return errors.New("Codex returned an invalid reasoning index.")
		}
		part := group + "/" + strconv.FormatInt(n, 10)
		if c.parts[id] == nil {
			c.parts[id] = map[string]bool{}
		}
		c.parts[id][part] = true
		key += "/" + part
	default:
		return nil
	}
	return c.p.put(key, kind, title, str(params, "delta"), "running", true,
		map[string]any{"type": method, "itemId": id})
}

func (c *codexInteractive) item(item map[string]any, completed bool) error {
	if str(item, "type") == "collabAgentToolCall" {
		return c.collaboration(item)
	}
	return putCodexItem(c.p, c.parts, item, completed)
}

func putCodexItem(p *adapter, trackedParts map[string]map[string]bool, item map[string]any, completed bool) error {
	id, typ := str(item, "id"), str(item, "type")
	key, status := "codex/item/"+id, "running"
	if completed {
		status = "completed"
	}
	switch str(item, "status") {
	case "completed":
		status = "completed"
	case "failed":
		status = "error"
	case "declined":
		status = "cancelled"
	}
	kind, title, text := "tool", typ, str(item, "text")
	switch typ {
	case "agentMessage":
		kind, title = "assistant", ""
	case "reasoning":
		if trackedParts[id] == nil {
			trackedParts[id] = map[string]bool{}
		}
		final := map[string]string{}
		for _, group := range []string{"summary", "content"} {
			parts, _ := item[group].([]any)
			for index, part := range parts {
				name := group + "/" + strconv.Itoa(index)
				final[name] = contentText(part)
				trackedParts[id][name] = true
				if err := p.put(key+"/"+name, "reasoning", "", final[name], status, false, nil); err != nil {
					return err
				}
			}
		}
		if completed {
			for name := range trackedParts[id] {
				if _, exists := final[name]; !exists {
					if err := p.put(key+"/"+name, "reasoning", "", "", status, false, nil); err != nil {
						return err
					}
				}
			}
		}
		return nil
	case "commandExecution":
		kind, title = "command", "Command"
		text = str(item, "command")
		if output := str(item, "aggregatedOutput"); output != "" {
			text += "\n" + output
		} else if !completed && text != "" {
			text += "\n"
		}
		if code, ok := item["exitCode"].(json.Number); ok {
			if value, err := code.Int64(); err == nil && value != 0 {
				status = "error"
			}
		}
	case "fileChange":
		title, text = "File changes", codexJSON(item["changes"])
	case "mcpToolCall":
		kind, title = "mcp", str(item, "server")+" / "+str(item, "tool")
		text = contentText(item["result"])
		if text == "" && item["result"] != nil {
			text = codexJSON(item["result"])
		}
		if text == "" && item["arguments"] != nil {
			text = codexJSON(item["arguments"])
		}
		if message := humanError(item["error"]); message != "" {
			text, status = message, "error"
		}
	default:
		return nil
	}
	return p.put(key, kind, title, text, status, false, map[string]any{"item": item})
}

func (c *codexInteractive) collaboration(item map[string]any) error {
	if str(item, "tool") != "spawnAgent" {
		return nil
	}
	itemID := str(item, "id")
	if !codexIdentifier(itemID) {
		return errors.New("Codex returned a subagent call without a valid identifier.")
	}
	title := str(item, "agentType")
	if title == "" {
		title = str(item, "prompt")
	}
	title = subagentTitle(title)
	receivers, _ := item["receiverThreadIds"].([]any)
	for _, value := range receivers {
		nativeID, _ := value.(string)
		if nativeID == "" || nativeID == c.threadID {
			continue
		}
		child, err := c.p.ensureSubagent(nativeID, title, "", "")
		if err != nil {
			return err
		}
		if child == nil {
			continue
		}
		parentKey := "codex/subagent/" + itemID + "/" + nativeID
		if c.children[nativeID] == nil {
			c.children[nativeID] = &codexSubagent{adapter: child, parentKey: parentKey, items: map[string]map[string]any{}, parts: map[string]map[string]bool{}}
			if err := child.resolvedSelection(child.model, child.effort); err != nil {
				return err
			}
		}
		status := "running"
		if str(item, "status") == "failed" {
			status = "error"
		}
		data := map[string]any{"harness": c.p.harness, "nativeCallId": itemID, "nativeAgentId": nativeID, "childSessionId": child.id, "item": item}
		if err := c.p.put(parentKey, "subagent", title, "", status, false, data); err != nil {
			return err
		}
	}
	return nil
}

func (c *codexInteractive) childFrame(child *codexSubagent, method string, params map[string]any) error {
	if method == "thread/tokenUsage/updated" {
		return child.adapter.setUsage(codexSessionUsage(object(params["tokenUsage"])))
	}
	if method == "error" {
		message := humanError(params["error"])
		if message == "" {
			message = "Codex reported a subagent error."
		}
		if err := child.adapter.put("", "error", "", message, "error", false, map[string]any{"type": method}); err != nil {
			return err
		}
		return c.p.finishSubagent(child.adapter, "error")
	}
	if method == "turn/started" {
		child.turnID = str(object(params["turn"]), "id")
		return nil
	}
	if method == "turn/completed" {
		turn := object(params["turn"])
		if child.turnID != "" && str(turn, "id") != child.turnID {
			return nil
		}
		status := "completed"
		switch str(turn, "status") {
		case "failed":
			status = "error"
		case "interrupted":
			status = "cancelled"
		}
		if err := c.p.finishSubagent(child.adapter, status); err != nil {
			return err
		}
		return c.p.put(child.parentKey, "subagent", "", "", status, false, map[string]any{"nativeAgentId": str(params, "threadId"), "childSessionId": child.adapter.id})
	}
	if method == "item/started" || method == "item/completed" {
		item := object(params["item"])
		id := str(item, "id")
		if !codexIdentifier(id) {
			return errors.New("Codex returned a subagent item without a valid identifier.")
		}
		child.items[id] = item
		if str(item, "type") == "contextCompaction" {
			return child.adapter.clearContextUsage()
		}
		return putCodexItem(child.adapter, child.parts, item, method == "item/completed")
	}
	id := str(params, "itemId")
	if !codexIdentifier(id) {
		return nil
	}
	key, kind, title := "codex/item/"+id, "", ""
	switch method {
	case "item/agentMessage/delta":
		kind = "assistant"
	case "item/commandExecution/outputDelta":
		kind, title = "command", "Command"
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		kind = "reasoning"
		field, group := "summaryIndex", "summary"
		if method == "item/reasoning/textDelta" {
			field, group = "contentIndex", "content"
		}
		index, valid := params[field].(json.Number)
		n, err := index.Int64()
		if !valid || err != nil || n < 0 || n > 10000 {
			return errors.New("Codex returned an invalid subagent reasoning index.")
		}
		part := group + "/" + strconv.FormatInt(n, 10)
		if child.parts[id] == nil {
			child.parts[id] = map[string]bool{}
		}
		child.parts[id][part] = true
		key += "/" + part
	default:
		return nil
	}
	return child.adapter.put(key, kind, title, str(params, "delta"), "running", true, map[string]any{"type": method, "itemId": id})
}

func (c *codexInteractive) request(frame map[string]any) error {
	id, method, params := frame["id"], str(frame, "method"), object(frame["params"])
	sourceID, validID := codexRPCID(id)
	rpcError := func(code int, message string) error {
		return c.send(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
	}
	if !validID {
		return errors.New("Codex returned an invalid server request identifier.")
	}
	if c.p.graphSealed() {
		return rpcError(-32000, "Graph activation is closing; no further interaction is permitted.")
	}
	if _, duplicate := c.requests[sourceID]; duplicate {
		return errors.New("Codex reused a server request identifier within a turn.")
	}
	bound := c.threadID != "" && c.turnID != "" && str(params, "threadId") == c.threadID && str(params, "turnId") == c.turnID
	requestItems := c.items
	if child := c.children[str(params, "threadId")]; child != nil {
		requestItems = child.items
		bound = child.turnID == "" || str(params, "turnId") == "" || str(params, "turnId") == child.turnID
	}
	if method == "mcpServer/elicitation/request" && params["turnId"] == nil {
		bound = c.threadID != "" && c.turnID != "" && str(params, "threadId") == c.threadID
	}
	if method == "mcpServer/elicitation/request" && c.p.graphOwnsNativeSession(str(params, "threadId")) {
		// Child calls in this owned activation keep the ordinary one-shot MCP
		// permission flow. No cross-thread remembered grant is manufactured.
		bound = true
	}
	if method == "execCommandApproval" || method == "applyPatchApproval" {
		if err := c.send(map[string]any{"id": id, "result": map[string]any{"decision": "denied"}}); err != nil {
			return err
		}
		return c.warning("Legacy Codex approval requests are not supported and were denied.")
	}
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "mcpServer/elicitation/request", "item/tool/requestUserInput":
		if !bound {
			return rpcError(-32602, "Request does not belong to the active thread and turn.")
		}
	default:
		return rpcError(-32601, "Unsupported Codex interactive request.")
	}
	if method == "item/tool/requestUserInput" {
		live := &atomic.Bool{}
		live.Store(true)
		c.requests[sourceID] = live
		reply := func(answers [][]string, cancelled bool) error {
			if c.p.turn.ctx.Err() != nil || !live.CompareAndSwap(true, false) {
				return errors.New("The Codex question is no longer active.")
			}
			result := map[string]any{}
			if !cancelled {
				questions, _ := params["questions"].([]any)
				for index, answer := range answers {
					result[str(object(questions[index]), "id")] = map[string]any{"answers": answer}
				}
			}
			return c.send(map[string]any{"id": id, "result": map[string]any{"answers": result}})
		}
		invalid := func() error {
			if err := reply(nil, true); err != nil {
				return err
			}
			return c.warning("Codex requested malformed or unsupported user input. No answers or consent were supplied.")
		}
		questions, ok := params["questions"].([]any)
		if !ok || len(questions) == 0 || len(questions) > 12 {
			return invalid()
		}
		req := QuestionRequest{SourceID: sourceID}
		for _, value := range questions {
			question := object(value)
			custom, validCustom := question["isOther"].(bool)
			secret, validSecret := question["isSecret"].(bool)
			header, validHeader := question["header"].(string)
			text, validText := question["question"].(string)
			if !validCustom || !validSecret || !validHeader || !validText {
				return invalid()
			}
			item := QuestionItem{ID: str(question, "id"), Header: header, Text: text, Custom: custom, Secret: secret}
			if question["options"] == nil {
				item.Custom = true
			} else {
				options, ok := question["options"].([]any)
				if !ok || len(options) > 64 {
					return invalid()
				}
				for _, value := range options {
					option := object(value)
					label, validLabel := option["label"].(string)
					description, validDescription := option["description"].(string)
					if !validLabel || !validDescription {
						return invalid()
					}
					item.Options = append(item.Options, QuestionOption{Label: label, Description: description})
				}
			}
			req.Items = append(req.Items, item)
		}
		if err := c.p.requestQuestion(req, reply); err != nil {
			return invalid()
		}
		return nil
	}

	req := Permission{Harness: "codex", SourceID: sourceID, Details: params,
		Decisions: []string{"once", "session", "always", "reject"}}
	var scope map[string]any
	positive := true
	result := func(allow bool) map[string]any {
		decision := "decline"
		if allow {
			decision = "accept"
		}
		return map[string]any{"decision": decision}
	}
	itemID := str(params, "itemId")
	if method != "mcpServer/elicitation/request" {
		started, ok := params["startedAtMs"].(json.Number)
		ms, err := started.Int64()
		positive = codexIdentifier(itemID) && ok && err == nil && ms >= 0
	}
	switch method {
	case "item/commandExecution/requestApproval":
		req.Kind, req.Title = "command", "Run command"
		command, workdir := str(params, "command"), str(params, "cwd")
		req.Command, req.Path = command, workdir
		if req.Path == "" {
			req.Path = c.cwd
		}
		network := object(params["networkApprovalContext"])
		validNetwork := network != nil && strings.TrimSpace(str(network, "host")) != "" && strings.TrimSpace(str(network, "protocol")) != ""
		positive = positive && (strings.TrimSpace(command) != "" || validNetwork)
		if params["cwd"] != nil && workdir == "" {
			positive = false
		}
		if params["networkApprovalContext"] != nil && !validNetwork {
			positive = false
		}
		if params["command"] != nil && command == "" {
			positive = false
		}
		if env := params["environmentId"]; env != nil && !codexIdentifier(str(params, "environmentId")) {
			positive = false
		}
		if permissions := params["additionalPermissions"]; permissions != nil && !codexPermissions(object(permissions)) {
			positive = false
		}
		if item := requestItems[itemID]; item != nil && str(item, "type") != "commandExecution" {
			positive = false
		}
		if decisions, supplied := params["availableDecisions"]; supplied {
			allowed := false
			list, _ := decisions.([]any)
			for _, decision := range list {
				if value, ok := decision.(string); ok && value == "accept" {
					allowed = true
				}
			}
			positive = positive && allowed
		}
		scope = map[string]any{"method": method, "command": params["command"], "cwd": params["cwd"],
			"environmentId": params["environmentId"], "additionalPermissions": params["additionalPermissions"],
			"networkApprovalContext": params["networkApprovalContext"], "threadCwd": c.cwd}
		req.Description = "Approve this action once, including all requested additional permissions together.\n" + codexJSON(scope)
		if command != "" {
			req.Patterns = []string{command}
		}
	case "item/fileChange/requestApproval":
		req.Kind, req.Title = "file", "Apply file changes"
		if root := params["grantRoot"]; root != nil && str(params, "grantRoot") == "" {
			positive = false
		}
		item := requestItems[itemID]
		if item != nil {
			req.Details = map[string]any{"request": params, "item": item}
		}
		changes, identifiable := codexChanges(item)
		if item != nil && str(item, "type") != "fileChange" {
			positive = false
		}
		if identifiable {
			scope = map[string]any{"method": method, "changes": changes, "grantRoot": params["grantRoot"], "cwd": c.cwd}
			req.Details = map[string]any{"request": params, "changes": changes}
			req.Description = "Approve only these file changes, including their diffs and rename destinations. No directory-wide grant is sent.\n" + codexJSON(changes)
			for _, value := range changes {
				change := object(value)
				req.Patterns = append(req.Patterns, str(change, "path"))
				if destination := str(object(change["kind"]), "move_path"); destination != "" {
					req.Patterns = append(req.Patterns, destination)
				}
			}
		} else {
			req.Decisions = []string{"once", "reject"}
			req.Description = "Exact file changes are unavailable. Approval applies once to this native request and cannot be remembered. No directory-wide grant is sent.\n" + codexJSON(req.Details)
		}
	case "item/permissions/requestApproval":
		req.Kind, req.Title, req.AllowLabel = "permissions", "Grant turn permissions", "Allow for this turn"
		permissions := object(params["permissions"])
		positive = positive && str(params, "cwd") != "" && codexPermissions(permissions)
		if env := params["environmentId"]; env != nil && !codexIdentifier(str(params, "environmentId")) {
			positive = false
		}
		scope = map[string]any{"method": method, "permissions": permissions, "cwd": params["cwd"], "environmentId": params["environmentId"]}
		req.Description = "These exact resources remain authorized for the rest of the current native turn, not just one operation. Deny entries and all other restrictions are preserved.\n" + codexJSON(scope)
		result = func(allow bool) map[string]any {
			granted := map[string]any{}
			if allow {
				granted = permissions
			}
			return map[string]any{"permissions": granted, "scope": "turn"}
		}
	case "mcpServer/elicitation/request":
		req.Kind, req.Title, req.Decisions = "mcp", "Approve MCP request", []string{"once", "reject"}
		result = func(allow bool) map[string]any {
			if allow {
				return map[string]any{"action": "accept", "content": map[string]any{}}
			}
			return map[string]any{"action": "decline", "content": nil}
		}
		positive = str(object(params["_meta"]), "codex_approval_kind") == "mcp_tool_call" &&
			str(params, "mode") == "form" && codexEmptyForm(object(params["requestedSchema"])) &&
			codexIdentifier(str(params, "serverName")) && strings.TrimSpace(str(params, "message")) != ""
		if !positive {
			if err := c.send(map[string]any{"id": id, "result": result(false)}); err != nil {
				return err
			}
			return c.warning("Unsupported MCP elicitation was declined. Forms requiring input, URL flows, and OpenAI forms are not supported.")
		}
		if c.p.ownsLinkedBridge(str(params, "serverName")) {
			// This owned server exposes only KLM's linked-conversation tools.
			// No tool-name inference from the human-readable message is needed.
			c.requests[sourceID] = &atomic.Bool{}
			return c.send(map[string]any{"id": id, "result": result(true)})
		}
		req.Description = "Server: " + str(params, "serverName") + "\n" + str(params, "message") +
			"\nThe request does not identify a canonical tool. Approval is one-shot and cannot be remembered."
	}
	if !positive {
		scope, req.Decisions = nil, []string{"reject"}
		req.Description = "This request cannot be safely approved because its action, identifiers, or permission semantics are unsupported or incomplete.\n" + codexJSON(params)
	}
	live := &atomic.Bool{}
	live.Store(true)
	c.requests[sourceID] = live
	return c.p.requestPermission(req, scope, func(allow bool) error {
		if allow && !positive {
			return errors.New("This Codex request cannot be approved safely.")
		}
		if c.p.turn.ctx.Err() != nil || !live.CompareAndSwap(true, false) {
			return errors.New("The Codex permission request is no longer active.")
		}
		if allow && c.p.graphSealed() {
			allow = false
		}
		return c.send(map[string]any{"id": id, "result": result(allow)})
	})
}

func codexChanges(item map[string]any) ([]any, bool) {
	changes, ok := item["changes"].([]any)
	if str(item, "type") != "fileChange" || !ok || len(changes) == 0 {
		return nil, false
	}
	for _, value := range changes {
		change := object(value)
		kind := object(change["kind"])
		if strings.TrimSpace(str(change, "path")) == "" {
			return nil, false
		}
		if _, ok := change["diff"].(string); !ok {
			return nil, false
		}
		switch str(kind, "type") {
		case "add", "delete":
			if kind["move_path"] != nil {
				return nil, false
			}
		case "update":
			if kind["move_path"] != nil && strings.TrimSpace(str(kind, "move_path")) == "" {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return changes, true
}

func codexEmptyForm(schema map[string]any) bool {
	if schema == nil || str(schema, "type") != "object" {
		return false
	}
	for key, value := range schema {
		switch key {
		case "type":
		case "title", "description", "$schema":
			if _, ok := value.(string); !ok {
				return false
			}
		case "properties":
			if properties := object(value); properties == nil || len(properties) != 0 {
				return false
			}
		case "required":
			if required, ok := value.([]any); !ok || len(required) != 0 {
				return false
			}
		case "additionalProperties":
			if _, ok := value.(bool); !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Validate only understood permission shapes, then send the original object
// unchanged: flattening filesystem entries would discard deny rules.
func codexPermissions(permissions map[string]any) bool {
	if permissions == nil || len(permissions) == 0 {
		return false
	}
	for key, value := range permissions {
		switch key {
		case "network":
			if value == nil {
				continue
			}
			network := object(value)
			if network == nil {
				return false
			}
			for field, enabled := range network {
				if field != "enabled" {
					return false
				}
				if _, ok := enabled.(bool); enabled != nil && !ok {
					return false
				}
			}
		case "fileSystem":
			if value == nil {
				continue
			}
			fs := object(value)
			if fs == nil {
				return false
			}
			for field, resources := range fs {
				switch field {
				case "read", "write":
					if resources == nil {
						continue
					}
					paths, ok := resources.([]any)
					if !ok {
						return false
					}
					for _, path := range paths {
						if path, ok := path.(string); !ok || strings.TrimSpace(path) == "" {
							return false
						}
					}
				case "globScanMaxDepth":
					number, ok := resources.(json.Number)
					depth, err := number.Int64()
					if !ok || err != nil || depth < 0 {
						return false
					}
				case "entries":
					entries, ok := resources.([]any)
					if !ok {
						return false
					}
					for _, value := range entries {
						entry := object(value)
						access := str(entry, "access")
						if len(entry) != 2 || (access != "read" && access != "write" && access != "deny") {
							return false
						}
						path := object(entry["path"])
						if len(path) != 2 {
							return false
						}
						switch str(path, "type") {
						case "path":
							if strings.TrimSpace(str(path, "path")) == "" {
								return false
							}
						case "glob_pattern":
							if strings.TrimSpace(str(path, "pattern")) == "" {
								return false
							}
						case "special":
							special := object(path["value"])
							switch str(special, "kind") {
							case "root", "minimal", "tmpdir", "slash_tmp":
								if len(special) != 1 {
									return false
								}
							case "project_roots":
								if len(special) > 2 {
									return false
								}
								if _, ok := special["subpath"].(string); special["subpath"] != nil && !ok {
									return false
								}
							default:
								return false
							}
						default:
							return false
						}
					}
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
