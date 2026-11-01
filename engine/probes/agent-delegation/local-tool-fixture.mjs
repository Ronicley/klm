// Bounded installed-harness fixture: no user HOME, upstream model or real credentials.
// node local-tool-fixture.mjs <pi|opencode> <package-dir-or-exe> <approved-temp-parent>
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { EventEmitter } from 'node:events';
import { createServer } from 'node:http';
import { createInterface } from 'node:readline';
import { mkdtemp, mkdir, writeFile, appendFile, readFile, stat } from 'node:fs/promises';
import { writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as sleep } from 'node:timers/promises';

const [kind, executableArg, parentArg, assetsArg] = process.argv.slice(2);
assert(['pi', 'opencode'].includes(kind));
const parent = resolve(parentArg), executable = resolve(executableArg);
assert((await stat(parent)).isDirectory());
const root = await mkdtemp(join(parent, `delegation-${kind}-`));
const cwd = join(root, 'project'), home = join(root, 'home'), agent = join(root, 'agent');
for (const dir of [cwd, home, agent, join(root, 'tmp'), join(root, 'config'), join(root, 'data'), join(root, 'cache')]) await mkdir(dir, { recursive: true });
const began = Date.now(), lifetime = new AbortController(), bus = new EventEmitter();
const report = { kind, root, checks: [], providerRequests: [], gates: [], processes: [] };
let logs = Promise.resolve(), bytes = 0, plan = 'tool', modelSequence = 0, native, eventReader;
const events = [];
const elapsed = () => Date.now() - began;
const log = (type, value) => {
  const line = JSON.stringify({ ms: elapsed(), type, ...value }) + '\n'; bytes += Buffer.byteLength(line);
  if (bytes > 8 * 1024 * 1024) { lifetime.abort(new Error('log limit exceeded')); return; }
  logs = logs.then(() => appendFile(join(root, 'events.jsonl'), line));
};
const check = (name, extra = {}) => { const item = { name, ms: elapsed(), ...extra }; report.checks.push(item); log('check', item); console.log(JSON.stringify(item)); };
function bounded(promise, ms, label, cleanup = false) {
  let timer, onAbort;
  return Promise.race([promise, new Promise((_, reject) => {
    onAbort = () => reject(lifetime.signal.reason ?? new Error('aborted'));
    if (!cleanup) { if (lifetime.signal.aborted) return onAbort(); lifetime.signal.addEventListener('abort', onAbort, { once: true }); }
    timer = setTimeout(() => { const error = new Error(`${label} deadline ${ms}ms`); if (!cleanup) lifetime.abort(error); reject(error); }, ms);
  })]).finally(() => { clearTimeout(timer); lifetime.signal.removeEventListener('abort', onAbort); });
}
function wait(predicate, after = 0, ms = 15000) {
  const found = events.slice(after).find(predicate); if (found) return Promise.resolve(found);
  let listener;
  return bounded(new Promise(resolve => { listener = e => { if (predicate(e)) resolve(e); }; bus.on('event', listener); }), ms, 'event wait').finally(() => bus.off('event', listener));
}
function emit(value) { if (events.length >= 4000) { lifetime.abort(new Error('event limit')); return; } events.push(value); log('event', { value }); bus.emit('event', value); }
const delay = ms => sleep(ms, undefined, { signal: lifetime.signal });
const gateWaiters = new Map();
const gates = [];
const managed = assetsArg ? JSON.parse(await readFile(join(resolve(assetsArg), 'metadata.json'), 'utf8')) : undefined;
if (managed) report.managedSpecDigest = managed.manifest.specDigest;
const service = createServer(async (req, res) => {
  try {
    if (req.url === '/mcp' && managed) {
      assert.equal(req.headers.authorization, 'Bearer fixture-owned-delegation-probe');
      let raw = ''; for await (const data of req) { raw += data; assert(raw.length < 2 * 1024 * 1024); }
      const msg = JSON.parse(raw), params = msg.params ?? {};
      const reply = result => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result })); };
      if (msg.method === 'klm/delegation/ready') {
        assert.equal(params.specDigest, managed.manifest.specDigest);
        reply({ version: 1, specDigest: params.specDigest, maxChildren: 8, maxConcurrency: 4, activeBatchLimit: 1 }); emit({ type: 'managed/ready' }); return;
      }
      if (msg.method === 'klm/delegation/gate') { reply({ allowed: true }); return; }
      if (msg.method === 'klm/delegation/admit') {
        let gate = gates.find(gate => gate.request?.operationId === params.request.operationId);
        if (!gate) { const id = gates.length + 1; gate = { id, admittedMs: elapsed(), released: false, cancelled: false, request: params.request }; gates.push(gate); report.gates.push(gate); emit({ type: 'gate/admitted', id }); }
        reply({ accepted: true, version: 1, batchId: `batch-${gate.id}`, operationId: gate.request.operationId, childCount: gate.request.children.length, maxConcurrency: 4 }); return;
      }
      const gate = gates.find(gate => `batch-${gate.id}` === params.batchId); assert(gate);
      if (msg.method === 'klm/delegation/cancel') { gate.cancelled = true; gate.cancelledMs = elapsed(); emit({ type: 'gate/cancelled', id: gate.id }); reply({ cancelled: true }); return; }
      assert.equal(msg.method, 'klm/delegation/wait');
      const send = () => reply(gate.released ? { revision: '2', state: 'settled', result: { version: 1, batchId: params.batchId, operationId: gate.request.operationId, outcome: 'completed', isError: false, canContinue: true, children: gate.request.children.map((child, index) => ({ key: child.key, invocationId: `inv-${gate.id}-${index}`, childSessionId: `child-${gate.id}-${index}`, harness: kind, model: 'fixture', effort: '', outcome: 'completed', result: 'fixture-all-children-terminal', finality: { nativeSettled: true, processesDrained: true, toolCallsSettled: true } })) } } : { revision: '1', state: 'waiting' });
      if (gate.released) send();
      else { const timer = setTimeout(() => { gateWaiters.delete(gate.id); send(); }, 800); gateWaiters.set(gate.id, () => { clearTimeout(timer); gateWaiters.delete(gate.id); send(); }); res.on('close', () => { clearTimeout(timer); gateWaiters.delete(gate.id); }); }
      return;
    }
    if (req.url === '/admit') {
      const id = gates.length + 1; const gate = { id, admittedMs: elapsed(), released: false, cancelled: false };
      gates.push(gate); report.gates.push(gate); emit({ type: 'gate/admitted', id });
      res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ id })); return;
    }
    const match = /^\/(wait|cancel)\/(\d+)$/.exec(req.url);
    if (match) {
      const gate = gates[Number(match[2]) - 1]; assert(gate);
      if (match[1] === 'cancel') { gate.cancelled = true; gate.cancelledMs = elapsed(); emit({ type: 'gate/cancelled', id: gate.id }); res.end('{}'); return; }
      const reply = () => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ done: gate.released })); };
      if (gate.released) reply();
      else { const timer = setTimeout(() => { gateWaiters.delete(gate.id); reply(); }, 800); gateWaiters.set(gate.id, () => { clearTimeout(timer); gateWaiters.delete(gate.id); reply(); }); res.on('close', () => { clearTimeout(timer); gateWaiters.delete(gate.id); }); }
      return;
    }
    assert.equal(req.url, '/v1/chat/completions'); assert.equal(req.method, 'POST');
    assert(!req.headers.authorization || req.headers.authorization === 'Bearer fixture-only');
    let raw = ''; for await (const chunk of req) { raw += chunk; assert(raw.length < 4 * 1024 * 1024); }
    const body = JSON.parse(raw); const mode = plan; modelSequence++;
    const tools = (body.tools ?? []).map(t => t.function?.name ?? t.name);
    const info = { sequence: modelSequence, mode, tools, ms: elapsed(), model: body.model };
    report.providerRequests.push(info); log('provider', info);
    await writeFile(join(root, `provider-${modelSequence}.json`), JSON.stringify(body, null, 2));
    assert(!tools.includes('task') && !tools.includes('subagent'), 'native delegation still exposed');
    if (mode === 'tool') assert(tools.includes('subagent_run'), 'local tool absent in provider catalog');
    if (managed) assert.deepEqual(body.tools.find(t => t.function?.name === 'subagent_run').function.parameters, managed.schema, 'actual provider did not receive the complete frozen schema');
    plan = 'final';
    const base = { id: `fixture_${modelSequence}`, object: 'chat.completion.chunk', created: Math.floor(Date.now() / 1000), model: 'fixture' };
    const taskArgs = managed ? JSON.stringify({ operationId: `fixture-op-${modelSequence}`, children: [{ key: 'worker', task: 'Return the fixture result.' }] }) : '{}';
    const delta = mode === 'tool' ? { role: 'assistant', tool_calls: [{ index: 0, id: `fixture_call_${modelSequence}`, type: 'function', function: { name: 'subagent_run', arguments: taskArgs } }] } : { role: 'assistant', content: `fixture-final-${modelSequence}` };
    const chunks = [{ ...base, choices: [{ index: 0, delta, finish_reason: null }] }, { ...base, choices: [{ index: 0, delta: {}, finish_reason: mode === 'tool' ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 10, total_tokens: 20 } }];
    res.writeHead(200, { 'content-type': 'text/event-stream' }); res.end(chunks.map(value => `data: ${JSON.stringify(value)}\n\n`).join('') + 'data: [DONE]\n\n');
  } catch (error) { lifetime.abort(error); log('fixture-error', { message: error.message }); if (!res.headersSent) res.writeHead(500); res.end('fixture failed'); }
});
await new Promise(resolve => service.listen(0, '127.0.0.1', resolve));
const url = `http://127.0.0.1:${service.address().port}`;
if (managed) for (const name of ['delegation-protocol.mjs', 'delegation-pi.mjs', 'delegation-opencode.mjs']) {
  const source = await readFile(join(resolve(assetsArg), name), 'utf8');
  await writeFile(join(root, name), source.replaceAll('http://127.0.0.1:1/mcp', url + '/mcp'));
}
const env = {};
for (const key of ['SystemRoot', 'WINDIR', 'PATH', 'PATHEXT', 'COMSPEC']) if (process.env[key]) env[key] = process.env[key];
Object.assign(env, { HOME: home, USERPROFILE: home, APPDATA: join(home, 'AppData/Roaming'), LOCALAPPDATA: join(home, 'AppData/Local'), TEMP: join(root, 'tmp'), TMP: join(root, 'tmp'), XDG_CONFIG_HOME: join(root, 'config'), XDG_DATA_HOME: join(root, 'data'), XDG_CACHE_HOME: join(root, 'cache'), PI_CODING_AGENT_DIR: agent, KLM_USER_SKILLS_DIR: join(root, 'skills') });
const wrapperHelper = `
async function run(signal) {
  const call = async path => { const response = await fetch(${JSON.stringify(url)} + path, { signal: AbortSignal.any([signal, AbortSignal.timeout(2000)]) }); if (!response.ok) throw new Error('fixture gateway'); return response.json(); };
  const {id} = await call('/admit');
  try { while (!signal.aborted) { if ((await call('/wait/'+id)).done) return 'fixture-all-children-terminal'; } signal.throwIfAborted(); }
  catch (error) { try { await fetch(${JSON.stringify(url)}+'/cancel/'+id,{signal:AbortSignal.timeout(1000)}); } catch {} throw error; }
}
`;
function startProcess(command, args) {
  lifetime.signal.throwIfAborted();
  native = spawn(command, args, { cwd, env, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  const record = { pid: native.pid, startedMs: elapsed() }; report.processes.push(record);
  native.closed = new Promise(resolve => native.on('close', (code, signal) => { record.closedMs = elapsed(); record.code = code; record.signal = signal; native.isClosed = true; resolve(); }));
  native.on('error', error => lifetime.abort(error));
  native.stderr.on('data', chunk => log('stderr', { text: chunk.toString().slice(0, 8192) }));
  return record;
}
async function cleanupNative() {
  if (!native) return;
  const record = report.processes[0];
  if (!native.isClosed) {
    if (kind === 'pi') { native.stdin.end(); try { await bounded(native.closed, 1500, 'Pi EOF', true); } catch {} }
    if (!native.isClosed) {
      await new Promise(resolve => execFile('taskkill.exe', ['/PID', String(native.pid), '/T', '/F'], { timeout: 5000, windowsHide: true }, (error, stdout, stderr) => { record.termination = { error: error?.message, stdout, stderr }; record.terminatedPIDs = [...new Set(stdout.split('\n').flatMap(line => { const hit = /PID\s+(\d+)/i.exec(line); return hit ? [Number(hit[1])] : []; }))]; resolve(); }));
      await bounded(native.closed, 5000, 'owned PID tree close', true);
      assert(!record.termination.error, 'owned process-tree termination was not confirmed');
    }
  }
  let alive = true; try { process.kill(native.pid, 0); } catch { alive = false; }
  assert(!alive); record.pidAbsent = true;
  for (const pid of record.terminatedPIDs ?? []) {
    let exists = true; try { process.kill(pid, 0); } catch { exists = false; }
    assert(!exists, `owned terminated PID ${pid} still exists`);
  }
  record.observedTerminatedPIDsAbsent = true;
}
function release(id) { const gate = gates[id - 1]; gate.released = true; gate.releasedMs = elapsed(); gateWaiters.get(id)?.(); }
async function piScenario() {
  const metadata = JSON.parse(await readFile(join(executable, 'package.json'), 'utf8')); assert.equal(metadata.version, '0.85.1'); report.version = metadata.version;
  await writeFile(join(agent, 'models.json'), JSON.stringify({ providers: { fixture: { baseUrl: url + '/v1', api: 'openai-completions', apiKey: 'fixture-only', models: [{ id: 'fixture', name: 'Fixture', reasoning: false, input: ['text'], contextWindow: 16000, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }));
  const extension = join(root, 'tool.mjs');
  if (managed) await writeFile(extension, `export {default} from './delegation-pi.mjs';`);
  else await writeFile(extension, wrapperHelper + `
export default function(pi) {
 pi.registerTool({name:'subagent_run',label:'Fixture',description:'Local fixture blocking tool',parameters:{type:'object',properties:{},additionalProperties:false},executionMode:'sequential',async execute(_id,_args,signal){return {content:[{type:'text',text:await run(signal)}],details:{}};}});
 pi.on('session_start', (_e,ctx)=>{ pi.setActiveTools(['subagent_run']); ctx.ui.notify('fixture-ready:'+JSON.stringify({active:pi.getActiveTools(),all:pi.getAllTools().map(t=>t.name)}),'info'); });
}
`);
  startProcess(process.execPath, [join(executable, 'dist/cli.js'), '--mode', 'rpc', '--offline', '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes', '--extension', extension, '--model', 'fixture/fixture', '--session', join(root, 'session.jsonl')]);
  const lines = createInterface({ input: native.stdout }); lines.on('line', line => { try { emit(JSON.parse(line)); } catch (error) { lifetime.abort(error); } });
  let seq = 0;
  const send = value => { lifetime.signal.throwIfAborted(); native.stdin.write(JSON.stringify(value) + '\n'); };
  const rpc = async type => { const id = `rpc-${++seq}`; send({ id, type }); const r = await wait(e => e.type === 'response' && e.id === id); assert(r.success, JSON.stringify(r)); return r.data; };
  if (managed) { await wait(e => e.type === 'managed/ready'); check('actual-versioned-pi-asset-ready', { specDigest: managed.manifest.specDigest }); }
  else {
    const ready = await wait(e => e.type === 'extension_ui_request' && e.message?.startsWith('fixture-ready:'));
    const inventory = JSON.parse(ready.message.slice('fixture-ready:'.length));
    assert.deepEqual(inventory.active, ['subagent_run']); assert(!inventory.all.includes('subagent'));
    check('local-sequential-tool-registered-native-delegation-suppressed', inventory);
  }
  let boundary = events.length; send({ id: 'prompt-1', type: 'prompt', message: 'Fixture first blocking operation.' });
  await wait(e => e.type === 'gate/admitted' && e.id === 1);
  await delay(2000); const before = modelSequence; const state = await rpc('get_state');
  assert.equal(before, 1); assert.equal(modelSequence, before); assert(!events.slice(boundary).some(e => e.type === 'agent_settled'));
  check('pending-local-promise-reader-responsive-no-next-model', { pendingMs: 2000, isStreaming: state.isStreaming, providerRequests: modelSequence });
  release(1); await wait(e => e.type === 'agent_settled', boundary); assert.equal(modelSequence, 2);
  check('consolidated-result-continuation', { providerRequests: modelSequence });
  boundary = events.length; plan = 'tool'; send({ id: 'prompt-2', type: 'prompt', message: 'Fixture cancel blocking operation.' });
  await wait(e => e.type === 'gate/admitted' && e.id === 2); await rpc('get_state');
  const atCancel = modelSequence; await rpc('clear_queue'); await rpc('abort');
  await wait(e => e.type === 'gate/cancelled' && e.id === 2, boundary); await wait(e => e.type === 'agent_settled', boundary);
  await delay(300); assert.equal(modelSequence, atCancel); assert.equal(gates.length, 2);
  check('pending-cancel-no-replay-reader-active', { providerRequests: modelSequence });
  boundary = events.length; plan = 'final'; send({ id: 'prompt-3', type: 'prompt', message: 'Fixture explicit follow-up after cancellation.' });
  await wait(e => e.type === 'agent_settled', boundary); assert.equal(modelSequence, atCancel + 1); assert.equal(gates.length, 2);
  check('explicit-followup-after-cancel', { providerRequests: modelSequence });
}
async function openCodeScenario() {
  report.version = '1.18.35'; // separately attest executable --version in command evidence
  const profile = join(root, 'config/opencode'); await mkdir(join(profile, 'node_modules'), { recursive: true });
  await writeFile(join(profile, 'package.json'), JSON.stringify({ private: true }));
  await writeFile(join(profile, 'package-lock.json'), JSON.stringify({ lockfileVersion: 3, packages: { '': { dependencies: { '@opencode-ai/plugin': '1.18.35' } } } }));
  const plugin = join(root, 'tool.mjs');
  if (managed) await writeFile(plugin, `export {default} from './delegation-opencode.mjs';`);
  else await writeFile(plugin, wrapperHelper + `
export default async () => ({
 tool:{subagent_run:{description:'Local blocking fixture tool',args:{},async execute(_args,ctx){return run(ctx.abort);}}},
 config:async config=>{config.permission={...config.permission,task:'deny'};config.tools={...config.tools,task:false};for(const agent of Object.values(config.agent??{})){agent.permission={...agent.permission,task:'deny'};agent.tools={...agent.tools,task:false};}},
 'chat.message':async (_input,output)=>{output.message.tools={...output.message.tools,task:false};},
 'tool.execute.before':async input=>{if(input.tool==='task')throw new Error('Native task disabled by owned fixture');},
});
`);
  const config = { model: 'fixture/fixture', small_model: 'fixture/fixture', enabled_providers: ['fixture'], plugin: [pathToFileURL(plugin).href], permission: { '*': 'allow', task: 'deny' }, tools: { task: false }, agent: { build: { mode: 'primary', permission: { '*': 'allow', task: 'deny' }, tools: { task: false } }, general: { disable: true }, explore: { disable: true } }, provider: { fixture: { npm: '@ai-sdk/openai-compatible', name: 'Fixture', options: { baseURL: url + '/v1', apiKey: 'fixture-only' }, models: { fixture: { name: 'Fixture', limit: { context: 16000, output: 1024 } } } } } };
  Object.assign(env, { OPENCODE_CONFIG_CONTENT: JSON.stringify(config), OPENCODE_CONFIG_DIR: profile, OPENCODE_TEST_HOME: home, OPENCODE_DISABLE_PROJECT_CONFIG: 'true', OPENCODE_DISABLE_EXTERNAL_SKILLS: 'true', OPENCODE_DISABLE_AUTOUPDATE: 'true', OPENCODE_DISABLE_DEFAULT_PLUGINS: 'true', OPENCODE_DISABLE_MODELS_FETCH: 'true', OPENCODE_DB: join(root, 'opencode.db') });
  startProcess(executable, ['serve', '--hostname', '127.0.0.1', '--port', '0', '--mdns=false']);
  const lines = createInterface({ input: native.stdout }); lines.on('line', line => { log('stdout', { text: line.slice(0, 8192) }); const match = /opencode server listening on (http:\/\/127\.0\.0\.1:\d+)/.exec(line); if (match) emit({ type: 'server/ready', base: match[1] }); });
  const { base } = await wait(e => e.type === 'server/ready', 0, 25000);
  async function http(method, path, body) { lifetime.signal.throwIfAborted(); const res = await fetch(base + path, { method, headers: { 'content-type': 'application/json', 'x-opencode-directory': cwd }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.any([lifetime.signal, AbortSignal.timeout(10000)]) }); const text = await res.text(); assert(res.ok, `${path}: ${res.status} ${text.slice(0, 1000)}`); return text ? JSON.parse(text) : undefined; }
  const health = await http('GET', '/global/health'); assert.equal(health.version, '1.18.35');
  await writeFile(join(root, 'effective-config.json'), JSON.stringify(await http('GET', '/config'), null, 2));
  await writeFile(join(root, 'effective-agents.json'), JSON.stringify(await http('GET', '/agent'), null, 2));
  const stream = new AbortController();
  eventReader = (async () => { const response = await fetch(base + '/event', { headers: { 'x-opencode-directory': cwd }, signal: AbortSignal.any([stream.signal, lifetime.signal]) }); assert(response.ok); let buffer = ''; for await (const bytes of response.body) { buffer += Buffer.from(bytes).toString(); assert(buffer.length < 2 * 1024 * 1024); let end; while ((end = buffer.indexOf('\n\n')) >= 0) { const frame = buffer.slice(0, end); buffer = buffer.slice(end + 2); const data = frame.split('\n').filter(line => line.startsWith('data:')).map(line => line.slice(5).trim()).join('\n'); if (data) emit(JSON.parse(data)); } } })().catch(error => { if (!lifetime.signal.aborted && !stream.signal.aborted) lifetime.abort(error); });
  const ids = await http('GET', '/experimental/tool/ids'); assert(ids.includes('subagent_run'));
  check('local-plugin-tool-registered', { version: health.version, ids });
  const session = await http('POST', '/session', { title: 'Disposable delegation fixture' }); report.nativeSessionId = session.id;
  const prompt = text => http('POST', `/session/${session.id}/prompt_async`, { model: { providerID: 'fixture', modelID: 'fixture' }, agent: 'build', parts: [{ type: 'text', text }] });
  const idle = e => (e.type === 'session.idle' || e.type === 'session.status' && e.properties.status?.type === 'idle') && e.properties.sessionID === session.id;
  let boundary = events.length; await prompt('Fixture blocking operation.'); await wait(e => e.type === 'gate/admitted' && e.id === 1);
  await delay(2000); const status = await http('GET', '/session/status'); assert.equal(modelSequence, 1); assert(!events.slice(boundary).some(idle));
  check('pending-local-promise-reader-responsive-no-next-model', { pendingMs: 2000, status: status[session.id], providerRequests: modelSequence });
  release(1); await wait(idle, boundary); assert.equal(modelSequence, 2);
  check('consolidated-result-continuation-native-task-absent', { providerRequests: modelSequence, tools: report.providerRequests[0].tools });
  boundary = events.length; plan = 'tool'; await prompt('Fixture cancel blocking operation.'); await wait(e => e.type === 'gate/admitted' && e.id === 2);
  const atCancel = modelSequence; await http('POST', `/session/${session.id}/abort`); await wait(e => e.type === 'gate/cancelled' && e.id === 2, boundary); await wait(idle, boundary);
  await delay(300); assert.equal(modelSequence, atCancel); assert.equal(gates.length, 2);
  check('pending-cancel-no-replay-reader-active', { providerRequests: modelSequence });
  boundary = events.length; plan = 'final'; await prompt('Fixture explicit follow-up after cancellation.'); await wait(idle, boundary); assert.equal(modelSequence, atCancel + 1); assert.equal(gates.length, 2);
  check('explicit-followup-after-cancel', { providerRequests: modelSequence }); stream.abort(); await eventReader;
}
let watchdog;
try {
  console.log(JSON.stringify({ root, kind, deadlineMs: 90000 }));
  await bounded(kind === 'pi' ? piScenario() : openCodeScenario(), 90000, 'entire scenario');
  report.outcome = 'passed';
} catch (error) { report.outcome = 'failed'; report.error = { message: error.message, stack: error.stack }; process.exitCode = 1; }
finally {
  lifetime.abort(new Error('fixture end; prohibit further dispatch'));
  watchdog = setTimeout(() => { report.outcome = 'cleanup_unconfirmed'; report.cleanupError = 'cleanup deadline'; writeFileSync(join(root, 'report.json'), JSON.stringify(report, null, 2)); process.exit(2); }, 15000);
  try { await cleanupNative(); } catch (error) { report.outcome = 'cleanup_unconfirmed'; report.cleanupError = error.message; process.exitCode = 1; }
  for (const release of gateWaiters.values()) release();
  service.closeAllConnections(); await new Promise(resolve => service.close(resolve));
  await eventReader; await logs; report.durationMs = elapsed();
  await writeFile(join(root, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ root, outcome: report.outcome, durationMs: report.durationMs, error: report.error?.message, cleanupError: report.cleanupError, processes: report.processes })); clearTimeout(watchdog);
}
