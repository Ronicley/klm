//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Self-contained native fixtures: no engine service, provider, credentials,
// installation, user Library, project or model request. Cleanup uses held pidfds
// for fixture identities and the real production supervisor's verified Close.
func init() {
	if len(os.Args) >= 3 && os.Args[1] == "__klm_test_linux_fixture" {
		os.Exit(linuxFixture(os.Args[2], os.Args[3:]))
	}
}
func linuxFixture(mode string, args []string) int {
	if mode == "marker" && len(args) == 1 {
		if os.WriteFile(args[0], []byte("native executed"), 0600) != nil {
			return 4
		}
		return 0
	}
	if mode == "workload" {
		time.Sleep(90 * time.Second)
		return 0
	}
	spawn := func() (int, error) {
		child := exec.Command(os.Args[0], "__klm_test_linux_fixture", "workload")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			return 0, err
		}
		pid := child.Process.Pid
		_ = child.Process.Release()
		return pid, nil
	}
	if mode == "root" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]int{"root": os.Getpid()})
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "spawn" {
				pid, err := spawn()
				if err != nil {
					return 4
				}
				_ = json.NewEncoder(os.Stdout).Encode(map[string]int{"child": pid})
			} else if scanner.Text() == "exit" {
				return 0
			}
		}
		return 0
	}
	if mode == "mcp" {
		pid, err := spawn()
		if err != nil || len(args) != 1 {
			return 4
		}
		if os.WriteFile(args[0], []byte(strconv.Itoa(pid)), 0600) != nil {
			return 4
		}
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var request map[string]any
			if json.Unmarshal(scanner.Bytes(), &request) != nil {
				return 5
			}
			if request["id"] == nil {
				continue
			}
			result := map[string]any{}
			switch str(request, "method") {
			case "initialize":
				result = map[string]any{"protocolVersion": str(object(request["params"]), "protocolVersion"), "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "owned-fixture", "version": "1"}}
			case "tools/list":
				result = map[string]any{"tools": []any{}}
			}
			if json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result}) != nil {
				return 6
			}
		}
		return 0
	}
	return 3
}

type linuxFixtureRoot struct {
	cmd     *exec.Cmd
	owner   *ownedProcess
	input   io.WriteCloser
	output  io.ReadCloser
	scanner *bufio.Scanner
	rootFD  int
	childFD int
}

func requireLinuxNonroot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Fatal("run this isolated fixture as the actual nonroot engine user, never root")
	}
}
func fixtureGroup(t *testing.T) *runtimeProcessGroup {
	t.Helper()
	g, err := newRuntimeProcessGroup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Errorf("owned fixture domain did not drain: %v", err)
		}
	})
	return g
}
func fixtureRoot(t *testing.T, g *runtimeProcessGroup) *linuxFixtureRoot {
	t.Helper()
	cmd := exec.Command(os.Args[0], "__klm_test_linux_fixture", "root")
	cmd.WaitDelay = time.Second
	owner, err := prepareSessionOwnedProcess(cmd, g)
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	f := &linuxFixtureRoot{cmd: cmd, owner: owner, input: input, output: output, scanner: bufio.NewScanner(output), rootFD: -1, childFD: -1}
	t.Cleanup(func() {
		input.Close()
		_ = owner.Close()
		_ = cmd.Wait()
		output.Close()
		for _, fd := range []int{f.rootFD, f.childFD} {
			if fd >= 0 {
				_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
				unix.Close(fd)
			}
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Attach(); err != nil {
		t.Fatal(err)
	}
	first := f.frame(t)
	if first["root"] != owner.nativePID {
		t.Fatal("proxy/native identity was not bound before readiness")
	}
	f.rootFD, err = unix.PidfdOpen(first["root"], 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *linuxFixtureRoot) frame(t *testing.T) map[string]int {
	t.Helper()
	if !f.scanner.Scan() {
		t.Fatalf("fixture native output closed: %v", f.scanner.Err())
	}
	var p map[string]int
	if err := json.Unmarshal(f.scanner.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *linuxFixtureRoot) spawn(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(f.input, "spawn\n"); err != nil {
		t.Fatal(err)
	}
	pid := f.frame(t)["child"]
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.childFD = fd
}
func pidfdExited(t *testing.T, fd int) bool {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 0); err != nil {
		t.Fatal(err)
	}
	return fds[0].Revents&unix.POLLIN != 0
}
func requirePidfdExit(t *testing.T, fd int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pidfdExited(t, fd) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("exact fixture process identity did not exit")
}

func TestLinuxManagedChatGraphBoundary(t *testing.T) {
	requireLinuxNonroot(t)
	t.Setenv("KLM_USER_SKILLS_DIR", t.TempDir())
	a := &app{binaries: map[string]binary{}}
	for _, harness := range []string{"pi", "opencode", "codex"} {
		if err := CheckGraphAdapter(harness); err == nil {
			t.Fatalf("Linux graph admission was enabled for %s", harness)
		}
		snapshot, err := a.captureCapabilities(context.Background(), CapabilitySelection{}, harness)
		if err != nil || snapshot == nil || snapshot.Adapter != libraryAdapterID(harness) {
			t.Fatalf("empty selection lost managed profile for %s: %v", harness, err)
		}
	}
}

func TestLinuxOwnedRuntimeLifecycle(t *testing.T) {
	requireLinuxNonroot(t)
	a, b := fixtureGroup(t), fixtureGroup(t)
	if err := a.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	rootA, rootB := fixtureRoot(t, a), fixtureRoot(t, b)
	a.MarkInfrastructure()
	rootA.spawn(t)
	rootB.spawn(t)
	a.EndTurn()
	if !a.HasWorkload() {
		t.Fatal("detached workload did not inhibit idle expiry")
	}
	if err := rootA.owner.Close(); err != nil {
		t.Fatal(err)
	}
	_ = rootA.cmd.Wait()
	requirePidfdExit(t, rootA.rootFD)
	if pidfdExited(t, rootA.childFD) {
		t.Fatal("root-only profile rotation killed carried workload")
	}
	if err := a.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	next := fixtureRoot(t, a)
	a.MarkInfrastructure()
	a.EndTurn()
	if !a.HasWorkload() || pidfdExited(t, rootA.childFD) {
		t.Fatal("new infrastructure replaced carried workload identity")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	requirePidfdExit(t, next.rootFD)
	requirePidfdExit(t, rootA.childFD)
	if pidfdExited(t, rootB.rootFD) || pidfdExited(t, rootB.childFD) {
		t.Fatal("one chat's Stop killed a neighboring owned domain")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	requirePidfdExit(t, rootB.childFD)
}
func TestLinuxOwnedRuntimeAdmissionAndEOF(t *testing.T) {
	requireLinuxNonroot(t)
	g := fixtureGroup(t)
	// Prepare reserves an IPC capability but cannot execute the native target.
	root := fixtureRoot(t, g)
	if _, err := io.WriteString(root.input, "exit\n"); err != nil {
		t.Fatal(err)
	}
	if err := root.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := root.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	next := fixtureRoot(t, g)
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	requirePidfdExit(t, next.rootFD)
	// Cancellation before proxy Start cannot cause a late native launch.
	other := fixtureGroup(t)
	marker := filepath.Join(t.TempDir(), "must-not-execute")
	cmd := exec.Command(os.Args[0], "__klm_test_linux_fixture", "marker", marker)
	owner, err := prepareSessionOwnedProcess(cmd, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("prepare executed native target before proxy admission")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err == nil {
		if err := owner.Attach(); err == nil {
			t.Fatal("cancelled proxy inherited a live admission descriptor")
		}
		_ = cmd.Wait()
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cancelled target executed after admission was revoked")
	}
}
func TestLinuxOwnedRuntimeSupervisorLoss(t *testing.T) {
	requireLinuxNonroot(t)
	g, err := newRuntimeProcessGroup()
	if err != nil {
		t.Fatal(err)
	}
	root := fixtureRoot(t, g)
	root.spawn(t)
	supervisorFD, err := unix.PidfdOpen(g.process.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(supervisorFD)
	if err := unix.PidfdSendSignal(supervisorFD, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	requirePidfdExit(t, supervisorFD)
	if err := g.BeginTurn(); err == nil {
		t.Fatal("lost supervisor was accepted for reuse")
	}
	if err := g.Close(); err == nil || g.drained {
		t.Fatal("lost supervisor falsely confirmed complete teardown")
	}
	if root.owner.Drained() {
		t.Fatal("nonexclusive/lost owner falsely advertised drainage")
	}
	// Accepted contract: descendants can survive supervisor destruction. Cleanup
	// is only of this fixture's held identity, never a global process/name scan.
	_ = unix.PidfdSendSignal(root.childFD, unix.SIGKILL, nil, 0)
	requirePidfdExit(t, root.childFD)
	// fixtureRoot's normal owner.Close must return an explicit error in this case.
	// It intentionally does not turn a lost domain into a new healthy one.
}
func TestLinuxOwnedMCPIndependentCleanup(t *testing.T) {
	requireLinuxNonroot(t)
	g := fixtureGroup(t)
	neighbor := fixtureRoot(t, g)
	neighbor.spawn(t)
	marker := filepath.Join(t.TempDir(), "mcp-workload.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := &app{}
	c := MCPConnectionSnapshot{ID: strings.Repeat("1", 32), Config: MCPConnectionConfig{Transport: "stdio", Auth: "none", Command: os.Args[0], Args: []string{"__klm_test_linux_fixture", "mcp", marker}}}
	live, err := a.connectMCP(ctx, c, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.close() })
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	defer unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	if err := live.close(); err != nil {
		t.Fatal(err)
	}
	requirePidfdExit(t, fd)
	if !live.owner.Drained() {
		t.Fatal("stdio MCP domain was not confirmed empty")
	}
	if pidfdExited(t, neighbor.rootFD) || pidfdExited(t, neighbor.childFD) {
		t.Fatal("MCP cleanup killed another session")
	}
}
func TestLinuxOwnedRuntimeConcurrentClose(t *testing.T) {
	requireLinuxNonroot(t)
	g := fixtureGroup(t)
	root := fixtureRoot(t, g)
	root.spawn(t)
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { results <- g.Close() }()
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	requirePidfdExit(t, root.childFD)
	if err := g.BeginTurn(); err == nil {
		t.Fatal("closed session domain was reused")
	}
}

// An opt-in narrow native extension/handshake probe. HOME is replaced by the
// publisher before launching the binary; no real provider/config/auth is read.
func TestLinuxPiManagedStartupProbe(t *testing.T) {
	if os.Getenv("KLM_RUN_LIBRARY_NATIVE_PROBES") != "1" {
		t.Skip("explicit isolated native probe only")
	}
	requireLinuxNonroot(t)
	_, bs := discoverHarnesses()
	b, ok := bs["pi"]
	if !ok {
		t.Fatal("pinned Pi unavailable in isolated probe environment")
	}
	if err := validatePiLibraryBinary(b); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []bool{false, true} {
		name := "empty"
		if selected {
			name = "selected"
		}
		t.Run(name, func(t *testing.T) { probeLinuxPiStartup(t, b, selected) })
	}
}

func probeLinuxPiStartup(t *testing.T, b binary, selected bool) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := &app{dir: root, ctx: ctx}
	p := &adapter{app: a, turn: &turn{ctx: ctx}, id: "linux-startup-fixture", cwd: cwd, harness: "pi", capabilities: &CapabilitySnapshot{Skills: []SkillSnapshot{}}, mcp: &mcpExecution{}}
	wanted := map[string]string{}
	if selected {
		source := filepath.Join(root, "source", "selected")
		if err := os.MkdirAll(source, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: selected\ndescription: disposable selected fixture\n---\nFixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		pkg, err := stableSkillPackage(filepath.Dir(source), "selected", false)
		if err != nil {
			t.Fatal(err)
		}
		skill, err := a.materializeSkill(pkg, false)
		if err != nil {
			t.Fatal(err)
		}
		p.capabilities.Skills = append(p.capabilities.Skills, skill)
		wanted["skill:selected"] = filepath.Join(a.skillSnapshotPath(skill), "SKILL.md")
		p.mcp.tools = []map[string]any{{"name": "ext_fixture", "description": "Selected fixture metadata only", "external": true, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}
	}
	bridge, err := newLinkedBridge(p, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.close()
	p.bridge = bridge
	config, _ := json.Marshal(map[string]any{"url": bridge.url, "token": bridge.token, "libraryUrl": bridge.libraryURL, "libraryToken": bridge.libraryToken, "tools": p.bridgeTools(), "graphNode": false, "nativeSubagents": false, "toolTimeoutMs": 10000})
	extension := strings.Replace(string(piPermissionsExtension), "/*KLM_LINKED_CONFIG*/{}", string(config), 1)
	path := filepath.Join(root, "owned-permissions.ts")
	if err := os.WriteFile(path, []byte(extension), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-session", "--extension", path}
	for _, skill := range p.capabilities.Skills {
		args = append(args, "--skill", a.skillSnapshotPath(skill))
	}
	proc, err := startInteractive(p.turn, b, args, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	if err := proc.Send(map[string]any{"id": "fixture-commands", "type": "get_commands"}); err != nil {
		t.Fatal(err)
	}
	ready, commands := false, false
	for !ready || !commands {
		select {
		case <-ctx.Done():
			t.Fatal("owned Pi startup/handshake timed out")
		case frame, ok := <-proc.Frames:
			if !ok {
				t.Fatal("Pi closed before managed extension handshake")
			}
			if str(frame, "message") == "klm.permissions.ready.v1" {
				ready = true
			}
			if str(frame, "id") == "fixture-commands" {
				if !truth(frame, "success") {
					t.Fatal("Pi command catalog failed")
				}
				items, _ := object(frame["data"])["commands"].([]any)
				for _, value := range items {
					item := object(value)
					info := object(item["sourceInfo"])
					if str(item, "name") == "llama" && str(info, "path") == "<inline:llama.cpp>" {
						continue
					}
					name := str(item, "name")
					if wanted[name] == "" || !piSamePath(wanted[name], str(info, "path")) {
						t.Fatal("unselected Pi command/skill loaded")
					}
					delete(wanted, name)
				}
				if len(wanted) != 0 {
					t.Fatal("selected Pi skill not loaded")
				}
				commands = true
			}
		}
	}
	if err := bridge.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	_ = proc.Close()
	if !proc.Drained() {
		t.Fatal("Pi startup-only domain teardown unconfirmed")
	}
	// No prompt, model turn or provider connection was submitted.
}
