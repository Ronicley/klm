package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeV2LibraryProjection(t *testing.T) {
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "project", "src")
	config := filepath.Join(home, ".config", "opencode")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(filepath.Dir(cwd), ".git", "HEAD"), "fixture")
	write(filepath.Join(config, "AGENTS.md"), "global")
	write(filepath.Join(filepath.Dir(cwd), "AGENTS.md"), "project")
	write(filepath.Join(cwd, "AGENTS.md"), "nested")
	write(filepath.Join(cwd, "CLAUDE.md"), "not V2 instructions")
	write(filepath.Join(filepath.Dir(cwd), "rules.md"), "configured")
	write(filepath.Join(config, "opencode.json"), `{"permissions":[{"action":"shell","resource":"*","effect":"deny"}],"policies":[{"action":"provider.use","resource":"fixture","effect":"deny"}],"mcp":{"servers":{"not-selected":{"type":"local","command":["forbidden"]}}},"plugins":["forbidden"],"providers":{"fixture":{"settings":{"apiKey":"{file:token}"}}}}`)
	write(filepath.Join(filepath.Dir(cwd), ".opencode", "opencode.json"), `{"default_agent":"plan","permissions":[{"action":"read","resource":"*.env","effect":"deny"}],"instructions":["rules.md"],"policies":[{"action":"provider.use","resource":"fixture","effect":"allow"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(config))
	for _, key := range []string{"OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_PERMISSION"} {
		t.Setenv(key, "")
	}
	got, err := openCodeV2LibraryProjection(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got["mcp"] != nil || got["plugins"] != nil || got["skills"] != nil || got["default_agent"] != "plan" {
		t.Fatal(got)
	}
	want := []any{filepath.Join(config, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md"), filepath.Join(filepath.Dir(cwd), "AGENTS.md"), filepath.Join(filepath.Dir(cwd), "rules.md")}
	if !reflect.DeepEqual(got["instructions"], want) {
		t.Fatalf("instructions=%v", got["instructions"])
	}
	rules := openCodeV2Rules(got["permissions"])
	policies := openCodeV2Rules(got["policies"])
	if len(rules) != 2 || str(object(rules[0]), "action") != "shell" || str(object(policies[len(policies)-1]), "effect") != "deny" {
		t.Fatal("native permission/policy precedence lost", got)
	}
	for _, content := range []string{`{"permission":{"bash":"deny"}}`, `{"default_agent":"custom"}`, `{"agents":{"plan":{"permission":{"edit":"deny"}}}}`} {
		t.Setenv("OPENCODE_CONFIG_CONTENT", content)
		if _, err := openCodeV2LibraryProjection(cwd); err == nil {
			t.Fatal("unsupported native config admitted", content)
		}
	}
}

func TestOpenCodeV2LibraryInventoryRejectsLeaks(t *testing.T) {
	p := &adapter{app: &app{}, turn: &turn{ctx: context.Background()}, openCodeRootAgent: "build", capabilities: &CapabilitySnapshot{}, bridge: &linkedBridge{tools: []map[string]any{{"name": "root"}, {"name": "ext_selected", "external": true}}}}
	for _, ids := range [][]string{{"read", "klm_linked_root", "klm_library_ext_selected", "opencode_session_move"}, {"read", "klm_linked_root"}, {"read", "klm_linked_root", "klm_library_ext_selected", "klm_library_unselected"}} {
		if p.verifyOpenCodeV2LibraryTools(ids) == nil {
			t.Fatal("invalid tool inventory admitted", ids)
		}
	}
	if err := p.verifyOpenCodeV2LibraryTools([]string{"read", "klm_linked_root", "klm_library_ext_selected"}); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"skill", "plugin", "role", "version"} {
		h := &openCodeHTTP{base: "http://fixture.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
			body := ""
			switch r.URL.Path {
			case "/api/info":
				body = `{"version":"2.2.7"}`
				if leak == "version" {
					body = `{"version":"2.2.8"}`
				}
			case "/api/skill":
				body = `{"data":[]}`
				if leak == "skill" {
					body = `{"data":[{"id":"unexpected","path":"skill://unexpected"}]}`
				}
			case "/api/plugin":
				body = `{"data":[{"id":"klm.policy","source":{"type":"local"}}]}`
				if leak == "plugin" {
					body = `{"data":[{"id":"rogue","source":{"type":"local"}}]}`
				}
			case "/api/agent":
				body = `{"data":[{"id":"build","permissions":[{"action":"skill","resource":"*","effect":"deny"},{"action":"klm_linked_*","resource":"*","effect":"allow"}]},{"id":"general","permissions":[{"action":"*","resource":"*","effect":"allow"}]}]}`
			default:
				t.Fatal(r.URL.Path)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		if err := p.verifyOpenCodeV2Library(h); err == nil {
			t.Fatal("leak admitted", leak)
		}
	}
}

func TestOpenCodeV2LibraryAdmission(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "opencode.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.2.6", "2.2.7", "2.2.8"} {
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"@opencode/cli","version":"`+version+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateOpenCodeV2LibraryBinary(binary{path: path}); (err == nil) != (version == "2.2.7") {
			t.Fatal(version, err)
		}
	}
	p := &adapter{app: &app{state: diskState{Native: map[string]nativeSession{"root": {ID: "ses_root"}}}}, id: "root", cwd: root, turn: &turn{ctx: context.Background()}, capabilities: &CapabilitySnapshot{Skills: []SkillSnapshot{{ID: "skill-library-manager", Name: "skill-library-manager", Internal: true}}}}
	for _, input := range []map[string]any{{"id": "unselected"}, {"id": "skill-library-manager"}, {"name": "skill-library-manager"}} {
		data, _ := json.Marshal(map[string]any{"sessionID": "ses_child", "toolCallId": "call", "toolName": "skill", "cwd": root, "input": input})
		if allowed, err := p.openCodePermissionGate(context.Background(), data); err == nil || allowed {
			t.Fatal("child/unselected skill admitted", input)
		}
	}
}

func TestOpenCodeV2LibraryPluginRoles(t *testing.T) {
	node, ok := executable("node")
	if !ok {
		t.Skip("Node unavailable")
	}
	const script = `import assert from 'node:assert/strict';
const source=process.argv[1];
for(const yolo of [false,true]) {
  const skills=[{id:'selected',path:'/captured/selected/SKILL.md'},{id:'internal',path:'/captured/internal/SKILL.md',internal:true}];
  const agents=[{id:'build',mode:'all',permissions:[{action:'shell',resource:'*',effect:'deny'}]},{id:'general',mode:'subagent',permissions:[]}];
  const tools=[{id:'read'},{id:'opencode_session_move'},{id:'opencode_session_create'},{id:'klm_linked_root'},{id:'klm_library_ext_selected'}];
  const catalog=[...skills,{id:'unselected',path:'/native/SKILL.md'}];
  const hooks=[];let inventory;
  const editor=(values)=>({list:()=>values,remove:id=>{const index=values.findIndex(v=>v.id===id);if(index>=0)values.splice(index,1)},update:(id,fn)=>fn(values.find(v=>v.id===id))});
  const ctx={
    tool:{transform:async fn=>fn(editor(tools)),hook:async()=>{},list:async()=>tools},
    agent:{list:async()=>({data:agents}),transform:async fn=>fn(editor(agents))},
    skill:{list:async()=>({data:catalog}),transform:async fn=>fn(editor(catalog))},
    mcp:{transform:async fn=>fn({list:()=>[],remove:()=>{}})},
    plugin:{list:async()=>({data:[{state:{status:'active'}}]})},
    session:{hook:async(kind,fn)=>hooks.push(fn),get:async({sessionID})=>({data:sessionID==='child'?{parentID:'root'}:{}})},
    rpc:{register:async(_schema,impl)=>{inventory=impl.inventory}},
  };
  const config={yolo,library:{root:'build',skills}};
  const code='async function graphPlugin(){}\nasync function delegationPlugin(){}\n'+source.replace('/*KLM_PERMISSION_CONFIG*/{}',JSON.stringify(config));
  const plugin=(await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'))).default;
  await plugin.setup(ctx);await inventory();
  assert.deepEqual(catalog.map(s=>s.id),['selected','internal']);
  assert.equal(agents[0].mode,'primary');
  assert(!tools.some(t=>t.id.startsWith('opencode_')));
  const effect=(agent,action,resource)=>agent.permissions.filter(r=>(r.action==='*'||r.action===action||r.action==='klm_linked_*'&&action.startsWith('klm_linked_'))&&(r.resource==='*'||r.resource===resource)).at(-1)?.effect;
  assert.equal(effect(agents[0],'skill','internal'),'allow');
  assert.equal(effect(agents[1],'skill','internal'),'deny');
  assert.equal(effect(agents[1],'skill','selected'),'allow');
  assert.equal(effect(agents[1],'klm_linked_root','*'),'deny');
  assert.equal(effect(agents[0],'shell','*'),yolo?'allow':'deny');
  const child={sessionID:'child',agent:'general',system:[],tools:{klm_linked_root:{},klm_library_ext_selected:{}}};
  for(const hook of hooks)await hook(child);
  assert.deepEqual(Object.keys(child.tools),['klm_library_ext_selected']);
  await assert.rejects(async()=>{for(const hook of hooks)await hook({...child,agent:'build'})},/managed root role/);
}
`
	cmd := exec.Command(node, "--input-type=module", "-e", script, openCodePermissionsPlugin)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin role check: %v\n%s", err, output)
	}
}

// Opt-in, disposable metadata-only probe. Never submits a model prompt.
func TestOpenCodeV2LibraryProbe(t *testing.T) {
	if os.Getenv("KLM_RUN_LIBRARY_NATIVE_PROBES") != "1" {
		t.Skip("opt-in native metadata probe")
	}
	b := binary{path: os.Getenv("KLM_OPENCODE_V2_PROBE_BINARY")}
	if err := validateOpenCodeV2LibraryBinary(b); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "project")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cwd, ".git", "HEAD"), "fixture")
	write(filepath.Join(cwd, "AGENTS.md"), "Fixture repository instructions")
	write(filepath.Join(home, ".config", "opencode", "plugins", "forbidden.js"), `throw new Error("unselected global plugin loaded")`)
	write(filepath.Join(cwd, ".opencode", "plugins", "forbidden.js"), `throw new Error("unselected project plugin loaded")`)
	write(filepath.Join(cwd, ".opencode", "skills", "unselected", "SKILL.md"), "---\nname: unselected\ndescription: forbidden\n---\nForbidden")
	write(filepath.Join(home, ".agents", "skills", "unselected", "SKILL.md"), "---\nname: unselected\ndescription: forbidden\n---\nForbidden")
	write(filepath.Join(cwd, "opencode.json"), `{"mcp":{"servers":{"unselected":{"type":"local","command":["not-a-real-executable"]}}}}`)
	for key, value := range map[string]string{"USERPROFILE": home, "HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_DATA_HOME": filepath.Join(root, "data"), "XDG_CACHE_HOME": filepath.Join(root, "cache"), "XDG_STATE_HOME": filepath.Join(root, "state"), "OPENCODE_TEST_HOME": home, "KLM_USER_SKILLS_DIR": filepath.Join(root, "user-skills"), "ProgramData": filepath.Join(root, "program-data")} {
		t.Setenv(key, value)
	}
	for _, key := range []string{"OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_DB", "OPENCODE_PERMISSION"} {
		t.Setenv(key, "")
	}
	nativeID := ""
	for _, scenario := range []struct {
		agent               string
		yolo, empty, denied bool
	}{{agent: "build"}, {agent: "plan", yolo: true}, {agent: "build", empty: true}, {agent: "build", denied: true}} {
		yolo := scenario.yolo
		settings := map[string]any{"default_agent": scenario.agent}
		if scenario.denied {
			settings["permissions"] = []any{map[string]any{"action": "skill", "resource": "selected", "effect": "deny"}}
		}
		encoded, _ := json.Marshal(settings)
		t.Setenv("OPENCODE_CONFIG_CONTENT", string(encoded))
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		a := &app{dir: filepath.Join(root, "engine"), ctx: ctx}
		if err := os.MkdirAll(a.dir, 0700); err != nil {
			t.Fatal(err)
		}
		p := &adapter{app: a, turn: &turn{ctx: ctx}, cwd: cwd, harness: "opencode", yolo: yolo, capabilities: &CapabilitySnapshot{Skills: []SkillSnapshot{}}, mcp: &mcpExecution{tools: []map[string]any{{"name": "ext_fixture", "description": "selected fixture", "external": true, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}}
		for _, id := range []string{"selected", "skill-library-manager"} {
			if scenario.empty {
				break
			}
			write(filepath.Join(root, "source", id, "SKILL.md"), "---\nname: "+id+"\ndescription: fixture\n---\nCaptured instructions\n")
			pkg, err := stableSkillPackage(filepath.Join(root, "source"), id, id == "skill-library-manager")
			if err != nil {
				t.Fatal(err)
			}
			skill, err := a.materializeSkill(pkg, id == "skill-library-manager")
			if err != nil {
				t.Fatal(err)
			}
			p.capabilities.Skills = append(p.capabilities.Skills, skill)
		}
		bridge, err := newLinkedBridge(p, ctx)
		if err != nil {
			t.Fatal(err)
		}
		p.bridge = bridge
		server, err := p.startOpenCodeServer(b, cwd, ctx)
		if scenario.denied {
			if server != nil {
				server.Close()
			}
			bridge.close()
			cancel()
			if err == nil {
				t.Fatal("native selected-skill denial bypassed")
			}
			t.Log("native selected-skill denial stopped preparation without prompting")
			continue
		}
		if err != nil {
			bridge.close()
			cancel()
			t.Fatalf("yolo=%t: %v", yolo, err)
		}
		var cfg []map[string]any
		if err := server.h.json(ctx, http.MethodGet, "/api/config", nil, &cfg); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(cfg)
		if !strings.Contains(string(data), "AGENTS.md") {
			t.Fatal("repository instructions missing")
		}
		var session map[string]any
		if nativeID == "" {
			if err := server.h.json(ctx, http.MethodPost, "/api/session", map[string]any{"title": "Disposable Library probe", "location": map[string]any{"directory": cwd}}, &session); err != nil {
				t.Fatal(err)
			}
			nativeID = str(session, "id")
		} else if err := server.h.json(ctx, http.MethodGet, "/api/session/"+nativeID, nil, &session); err != nil {
			t.Fatal(err)
		}
		if nativeID == "" || str(session, "id") != nativeID || !piSamePath(str(object(session["location"]), "directory"), cwd) {
			t.Fatal("native continuation/directory changed")
		}
		t.Logf("agent=%s yolo=%t empty=%t: snapshots, roles, gateways, owned plugin and unchanged native session verified", scenario.agent, yolo, scenario.empty)
		server.Close()
		bridge.close()
		cancel()
	}
}
