// Version-specific, disposable native catalog probe. No model turn is submitted.
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { createServer } from 'node:http';

const binary = resolve(process.argv[2]);
const root = await mkdtemp(join(tmpdir(), 'klm-codex-library-'));
let capturedModel;
let modelArrived;
const modelRequest = new Promise(resolve => { modelArrived = resolve; });
const gateway = createServer(async (req, res) => {
  if (req.method === 'GET') { res.writeHead(405); res.end(); return; }
  if (req.method === 'DELETE') { res.writeHead(204); res.end(); return; }
  let body = ''; for await (const data of req) body += data;
  const message = JSON.parse(body);
  if (req.url.includes('/responses')) {
    capturedModel = message; modelArrived(message);
    const response = { id: 'fixture-response', status: 'completed', output: [{ type: 'message', id: 'fixture-message', role: 'assistant', content: [{ type: 'output_text', text: 'fixture-only', annotations: [] }] }], usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } };
    res.setHeader('content-type', 'text/event-stream');
    res.end(`event: response.completed\ndata: ${JSON.stringify({ type: 'response.completed', response })}\n\n`);
    return;
  }
  if (!('id' in message)) { res.writeHead(202); res.end(); return; }
  const result = message.method === 'initialize'
    ? { protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: 'fixture', version: '1' } }
    : message.method === 'tools/list' ? { tools: [{ name: 'fixture_selected', description: 'Harmless fixture', inputSchema: { type: 'object', properties: {} } }] } : {};
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ jsonrpc: '2.0', id: message.id, result }));
});
await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve));
const cwd = join(root, 'project'), home = join(root, 'home'), codexHome = join(root, 'codex');
let child;
try {
  for (const dir of [cwd, home, codexHome, join(cwd, '.codex'), join(cwd, '.agents/skills/project-fixture'), join(home, '.agents/skills/user-fixture'), join(codexHome, 'skills/codex-fixture')]) await mkdir(dir, { recursive: true });
  for (const [dir, name] of [[join(cwd, '.agents/skills/project-fixture'), 'project-fixture'], [join(home, '.agents/skills/user-fixture'), 'user-fixture'], [join(codexHome, 'skills/codex-fixture'), 'codex-fixture']]) await writeFile(join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: harmless fixture\n---\nFixture\n`);
  const marker = join(root, 'unselected-started');
  const server = join(root, 'unselected.mjs');
  await writeFile(server, `import {writeFileSync} from 'node:fs'; writeFileSync(${JSON.stringify(marker)},'started'); process.stdin.resume();`);
  const q = value => JSON.stringify(value.replaceAll('\\', '/'));
  await writeFile(join(codexHome, 'config.toml'), `[projects.${q(cwd)}]\ntrust_level="trusted"\n[mcp_servers.unselected_user]\ncommand=${q(process.execPath)}\nargs=[${q(server)}]\n`);
  await writeFile(join(cwd, '.codex/config.toml'), `[mcp_servers.unselected_project]\ncommand=${q(process.execPath)}\nargs=[${q(server)}]\n`);
  await writeFile(join(cwd, 'AGENTS.md'), 'PROJECT_INSTRUCTION_SENTINEL\n');
  const env = {};
  for (const name of ['SystemRoot', 'WINDIR', 'PATH', 'PATHEXT', 'COMSPEC', 'TEMP', 'TMP']) if (process.env[name]) env[name] = process.env[name];
  Object.assign(env, { USERPROFILE: home, HOME: home, CODEX_HOME: codexHome, KLM_USER_SKILLS_DIR: join(root, 'klm-skills'), APPDATA: join(home, 'AppData/Roaming'), LOCALAPPDATA: join(home, 'AppData/Local') });
  const args = [
    '-c', 'features.skip_host_skill_discovery=true', '-c', 'skills.bundled.enabled=false', '-c', 'skills.include_instructions=false',
    '-c', 'features.plugins=false', '-c', 'features.skill_search=false', '-c', 'features.apps=false', '-c', 'agents.enabled=false',
    '-c', 'features.remote_models=false', '-c', 'features.external_migration=false',
    '-c', 'model_provider="klm_fixture"', '-c', 'model="fixture"',
    '-c', `model_providers.klm_fixture={name="Fixture",base_url="http://127.0.0.1:${process.argv.includes('--model-surface') ? gateway.address().port : 9}/v1",wire_api="responses",requires_openai_auth=false}`,
    '-c', `mcp_servers={klm_linked={url="http://127.0.0.1:${gateway.address().port}/mcp",enabled=true}}`,
    ...(process.argv.includes('--disable-known') ? ['-c', `mcp_servers.unselected_user={command=${q(process.execPath)},enabled=false}`, '-c', `mcp_servers.unselected_project={command=${q(process.execPath)},enabled=false}`] : []),
    'app-server', '--listen', 'stdio://',
  ];
  child = spawn(binary, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  const pending = new Map(); let sequence = 0; let diagnostic = '';
  const lines = createInterface({ input: child.stdout });
  child.stderr.on('data', data => { if (diagnostic.length < 4096) diagnostic += data.toString(); });
  child.on('exit', code => { for (const request of pending.values()) { clearTimeout(request.timer); request.reject(new Error(`Native process exited (${code}): ${diagnostic}`)); } pending.clear(); });
  lines.on('line', line => { const data = JSON.parse(line); if (pending.has(data.id)) { const { resolve, reject, timer } = pending.get(data.id); clearTimeout(timer); pending.delete(data.id); data.error ? reject(new Error(JSON.stringify(data.error))) : resolve(data.result); } });
  const call = (method, params) => new Promise((resolve, reject) => { const id = ++sequence; const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out: ${diagnostic}`)); }, 30000); pending.set(id, { resolve, reject, timer }); child.stdin.write(JSON.stringify({ id, method, params }) + '\n'); });
  await call('initialize', { clientInfo: { name: 'klm-disposable-library-probe', version: '1' }, capabilities: { experimentalApi: true } });
  child.stdin.write(JSON.stringify({ method: 'initialized' }) + '\n');
  const config = await call('config/read', { cwd, includeLayers: false });
  console.log('Effective MCP configuration:', Object.fromEntries(Object.entries(config.config.mcp_servers ?? {}).map(([id, cfg]) => [id, { enabled: cfg.enabled ?? true }])));
  const skills = await call('skills/list', { cwds: [cwd], forceReload: true });
  console.log('Native skills/list:', skills.data?.flatMap(group => group.skills.map(skill => ({ name: skill.name, enabled: skill.enabled, scope: skill.scope }))));
  if (process.argv.includes('--disable-known') || process.argv.includes('--thread-projection')) {
    const params = { cwd, ephemeral: true, approvalPolicy: 'on-request', sandbox: 'read-only' };
    if (process.argv.includes('--thread-projection')) {
      const servers = Object.fromEntries(Object.keys(config.config.mcp_servers ?? {}).map(name => [name, { command: process.execPath, enabled: false }]));
      servers.klm_linked = { url: `http://127.0.0.1:${gateway.address().port}/mcp`, enabled: true };
      params.config = { mcp_servers: servers };
    }
    const thread = await call('thread/start', params);
    console.log('Thread initialized without model turn:', !!thread.thread?.id);
    const inventory = await call('mcpServerStatus/list', { threadId: thread.thread.id, limit: 100 });
    console.log('Thread MCP inventory:', (inventory.data ?? []).map(server => ({ name: server.name, tools: Object.keys(server.tools ?? {}) })));
    if (process.argv.includes('--model-surface')) {
      await call('turn/start', { threadId: thread.thread.id, input: [{ type: 'text', text: 'Reply fixture-only. Do not execute tools.' }] });
      await Promise.race([modelRequest, new Promise((_, reject) => setTimeout(() => reject(new Error('Local inference fixture did not receive the model surface')), 12000))]);
      const text = JSON.stringify(capturedModel);
      console.log('Model surface retained project instructions:', text.includes('PROJECT_INSTRUCTION_SENTINEL'));
      console.log('Model surface includes unselected skill metadata:', ['project-fixture', 'codex-fixture', 'user-fixture', 'find-skills'].filter(name => text.includes(name)));
      console.log('Native tool names:', capturedModel.tools?.map(tool => tool.name ?? tool.type));
    }
  }
  let started = false; try { await readFile(marker); started = true; } catch {}
  console.log('Unselected process started:', started);
} finally {
  if (child && child.exitCode === null && child.signalCode === null) { child.kill(); await new Promise(resolve => child.once('close', resolve)); }
  await new Promise(resolve => gateway.close(resolve));
  await rm(root, { recursive: true, force: true });
}
