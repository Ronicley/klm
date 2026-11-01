// Disposable pinned Codex protocol fixture. Never uses an upstream model or user HOME.
// Usage: node codex-dynamic.mjs <codex.exe> <approved-temp-parent> [new-resume|adoption]
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { EventEmitter } from 'node:events';
import { createServer } from 'node:http';
import { createInterface } from 'node:readline';
import { mkdir, mkdtemp, readFile, writeFile, appendFile, stat } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import { setTimeout as sleep } from 'node:timers/promises';
import { writeFileSync } from 'node:fs';

const [binaryArg, parentArg, scenario = 'new-resume'] = process.argv.slice(2);
assert(binaryArg && parentArg && ['new-resume', 'adoption'].includes(scenario));
const binary = resolve(binaryArg), parent = resolve(parentArg);
assert((await stat(parent)).isDirectory());
const root = await mkdtemp(join(parent, `delegation-codex-${scenario}-`));
const home = join(root, 'home'), cwd = join(root, 'project'), nativeHome = join(root, 'codex');
for (const path of [home, cwd, nativeHome, join(root, 'tmp')]) await mkdir(path, { recursive: true });
const start = performance.now();
const lifetime = new AbortController();
const bus = new EventEmitter();
const report = { scenario, root, startedAt: new Date().toISOString(), processes: [], checks: [], providerRequests: [] };
let logWrites = Promise.resolve(), sequence = 0, client, providerSequence = 0, logBytes = 0;
let plan = { kind: 'final' }, fixtureFatal;
const clients = [];
const elapsed = () => Math.round(performance.now() - start);
function log(kind, value) {
  const record = { ms: elapsed(), kind, ...value };
  const line = JSON.stringify(record) + '\n';
  logBytes += Buffer.byteLength(line);
  if (logBytes > 8 * 1024 * 1024) { lifetime.abort(new Error('fixture log limit exceeded')); return; }
  logWrites = logWrites.then(() => appendFile(join(root, 'events.jsonl'), line));
}
function check(name, details = {}) { report.checks.push({ name, ms: elapsed(), ...details }); log('check', { name, ...details }); console.log(JSON.stringify({ name, ms: elapsed(), ...details })); }
function timeout(promise, ms, label, cleanup = false) {
  let timer, aborted;
  const deadline = new Promise((_, reject) => {
    aborted = () => reject(lifetime.signal.reason ?? new Error('scenario aborted'));
    if (!cleanup) {
      if (lifetime.signal.aborted) { aborted(); return; }
      lifetime.signal.addEventListener('abort', aborted, { once: true });
    }
    timer = setTimeout(() => { const error = new Error(`${label}: deadline ${ms}ms`); if (!cleanup) lifetime.abort(error); reject(error); }, ms);
  });
  return Promise.race([promise, deadline]).finally(() => { clearTimeout(timer); lifetime.signal.removeEventListener('abort', aborted); });
}
function event(source, predicate, ms = 15000, label = 'event') {
  const existing = source.frames.find(predicate);
  if (existing) return Promise.resolve(existing);
  let receive;
  return timeout(new Promise((resolve, reject) => {
    receive = value => { if (predicate(value)) { source.bus.off('frame', receive); resolve(value); } };
    source.bus.on('frame', receive);
    source.closed.then(() => { source.bus.off('frame', receive); reject(new Error(`${label}: process closed`)); });
  }), ms, label).finally(() => source.bus.off('frame', receive));
}
const delay = ms => sleep(ms, undefined, { signal: lifetime.signal });
const q = value => JSON.stringify(value.replaceAll('\\', '/'));
const toolSpec = name => ({ type: 'function', name, description: 'Deterministic local fixture tool; no file/process/network side effects.', inputSchema: { type: 'object', properties: { task: { type: 'string' } }, required: ['task'], additionalProperties: false }, deferLoading: false });
function toolNames(body) {
  return (body.tools ?? []).flatMap(tool => tool.type === 'namespace' ? tool.tools.map(child => `${tool.name}.${child.name}`) : [tool.name ?? tool.type]);
}
function responseOutput(mode, names) {
  if (mode.kind === 'delegate') {
    assert(names.includes('subagent_run'), 'dynamic tool must be in the actual provider request');
    return [
      { type: 'function_call', id: `fc_${providerSequence}_main`, call_id: `call_${providerSequence}_main`, name: 'subagent_run', arguments: JSON.stringify({ task: 'fixture-task' }), status: 'completed' },
      ...(mode.sibling ? [{ type: 'function_call', id: `fc_${providerSequence}_sibling`, call_id: `call_${providerSequence}_sibling`, name: 'fixture_sibling', arguments: JSON.stringify({ task: 'fixture-sibling' }), status: 'completed' }] : []),
    ];
  }
  return [{ type: 'message', id: `msg_${providerSequence}`, role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: `fixture-final-${providerSequence}`, annotations: [] }] }];
}
const server = createServer(async (req, res) => {
  try {
    assert.equal(req.method, 'POST'); assert.equal(req.url, '/v1/responses');
    assert(!req.headers.authorization, 'fixture provider must not receive credentials');
    let raw = ''; for await (const data of req) { raw += data; assert(raw.length <= 4 * 1024 * 1024); }
    const body = JSON.parse(raw), currentPlan = plan;
    providerSequence++;
    const names = toolNames(body);
    const summary = { sequence: providerSequence, ms: elapsed(), model: body.model, tools: names, plan: currentPlan.kind };
    report.providerRequests.push(summary); log('provider', summary);
    await writeFile(join(root, `provider-${providerSequence}.json`), JSON.stringify(body, null, 2));
    const output = responseOutput(currentPlan, names);
    if (currentPlan.kind === 'delegate') plan = { kind: 'final' };
    const response = { id: `resp_${providerSequence}`, object: 'response', created_at: Math.floor(Date.now() / 1000), status: 'completed', model: body.model, output, usage: { input_tokens: 10, output_tokens: 10, total_tokens: 20, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } } };
    const frames = [
      { type: 'response.created', response: { ...response, status: 'in_progress', output: [] } },
      ...output.flatMap((item, output_index) => [
        { type: 'response.output_item.added', output_index, item: item.type === 'function_call' ? { ...item, arguments: '', status: 'in_progress' } : item },
        { type: 'response.output_item.done', output_index, item },
      ]),
      { type: 'response.completed', response },
    ];
    res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
    res.end(frames.map(frame => `event: ${frame.type}\ndata: ${JSON.stringify(frame)}\n\n`).join(''));
    bus.emit('provider', summary);
  } catch (error) { fixtureFatal = error; log('provider-error', { error: error.message }); res.writeHead(500); res.end('fixture error'); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const port = server.address().port;
const environment = {};
for (const name of ['SystemRoot', 'WINDIR', 'PATH', 'PATHEXT', 'COMSPEC']) if (process.env[name]) environment[name] = process.env[name];
Object.assign(environment, { HOME: home, USERPROFILE: home, CODEX_HOME: nativeHome, APPDATA: join(home, 'AppData', 'Roaming'), LOCALAPPDATA: join(home, 'AppData', 'Local'), TEMP: join(root, 'tmp'), TMP: join(root, 'tmp'), KLM_USER_SKILLS_DIR: join(root, 'skills') });
const configText = `model_provider="klm_fixture"\nmodel="fixture"\n[model_providers.klm_fixture]\nname="Local deterministic fixture"\nbase_url="http://127.0.0.1:${port}/v1"\nwire_api="responses"\nrequires_openai_auth=false\nsupports_websockets=false\n[projects.${q(cwd)}]\ntrust_level="trusted"\n`;
await writeFile(join(nativeHome, 'config.toml'), configText);

async function openClient(label) {
  lifetime.signal.throwIfAborted();
  const args = [
    '-c', 'features.remote_models=false', '-c', 'features.external_migration=false',
    '-c', 'features.skip_host_skill_discovery=true', '-c', 'features.skill_search=false',
    '-c', 'skills.bundled.enabled=false', '-c', 'skills.include_instructions=false',
    '-c', 'features.plugins=false', '-c', 'features.remote_plugin=false', '-c', 'features.apps=false',
    '-c', 'features.connectors=false', '-c', 'features.hooks=false', '-c', 'features.multi_agent=false',
    '-c', 'features.multi_agent_v2=false', '-c', 'agents.enabled=false',
    'app-server', '--listen', 'stdio://',
  ];
  const process = spawn(binary, args, { cwd, env: environment, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  const entry = { label, pid: process.pid, startedMs: elapsed() }; report.processes.push(entry);
  const value = { process, entry, frames: [], bus: new EventEmitter(), pending: new Map() };
  clients.push(value);
  value.closed = new Promise(resolve => process.once('close', (code, signal) => { entry.closedMs = elapsed(); entry.code = code; entry.signal = signal; value.isClosed = true; resolve(); }));
  process.on('error', error => { value.spawnError = error; });
  process.stderr.on('data', chunk => log('stderr', { client: label, text: chunk.toString().slice(0, 8192) }));
  const lines = createInterface({ input: process.stdout });
  lines.on('line', line => {
    try {
      const frame = JSON.parse(line); log('native', { client: label, frame });
      if (value.frames.length >= 5000) throw new Error('native frame count limit exceeded');
      value.frames.push(frame);
      const waiter = value.pending.get(frame.id);
      if (waiter && !frame.method) { value.pending.delete(frame.id); frame.error ? waiter.reject(new Error(JSON.stringify(frame.error))) : waiter.resolve(frame.result); }
      value.bus.emit('frame', frame);
    } catch (error) { fixtureFatal = error; }
  });
  value.send = body => { lifetime.signal.throwIfAborted(); assert(!value.isClosed, 'send to closed process'); process.stdin.write(JSON.stringify(body) + '\n'); };
  value.call = (method, params, deadline = 15000) => {
    const id = `probe-${++sequence}`;
    const result = new Promise((resolve, reject) => value.pending.set(id, { resolve, reject }));
    value.send({ id, method, params });
    return timeout(result, deadline, method).finally(() => value.pending.delete(id));
  };
  await value.call('initialize', { clientInfo: { name: 'klm-agent-delegation-fixture', version: '1' }, capabilities: { experimentalApi: true } });
  value.send({ method: 'initialized' });
  return value;
}
const baseParams = () => ({ cwd, model: 'fixture', modelProvider: 'klm_fixture', approvalPolicy: 'never', sandbox: 'read-only', allowProviderModelFallback: false });
async function begin(value, threadId, label) {
  const result = await value.call('turn/start', { threadId, input: [{ type: 'text', text: label }] });
  return result.turn.id;
}
async function finish(value, threadId, turnId) {
  const frame = await event(value, frame => frame.method === 'turn/completed' && frame.params.threadId === threadId && frame.params.turn.id === turnId, 20000, 'turn completion');
  return frame.params.turn;
}
async function endClient(value) {
  if (!value.isClosed) {
    value.process.stdin.end();
    try { await timeout(value.closed, 5000, 'native EOF shutdown', true); value.entry.cleanup = 'EOF+close'; }
    catch {
    await new Promise(resolve => execFile('taskkill.exe', ['/PID', String(value.process.pid), '/T', '/F'], { timeout: 5000, windowsHide: true }, (error, stdout, stderr) => { value.entry.taskkill = { error: error?.message, stdout, stderr }; resolve(); }));
      await timeout(value.closed, 5000, 'owned native process teardown', true); value.entry.cleanup = 'owned PID tree termination+close';
    }
  }
  let alive = true; try { globalThis.process.kill(value.entry.pid, 0); } catch { alive = false; }
  assert(!alive, 'owned native PID still exists'); value.entry.pidAbsent = true;
}
async function newResumeScenario() {
  client = await openClient('new');
  const started = await client.call('thread/start', { ...baseParams(), dynamicTools: [toolSpec('subagent_run'), toolSpec('fixture_sibling')] });
  const threadId = started.thread.id; report.threadId = threadId;
  plan = { kind: 'delegate', sibling: true };
  const turnId = await begin(client, threadId, 'Fixture first delegation with one same-response sibling.');
  const request = await event(client, frame => frame.method === 'item/tool/call' && frame.params.tool === 'subagent_run', 15000, 'dynamic request');
  assert.equal(request.params.threadId, threadId); assert.equal(request.params.turnId, turnId);
  // The pin's DynamicToolHandler does not opt into parallel tool dispatch.
  // Do not assume a sibling already emitted by the fixture has started.
  check('dynamic-tool-pending', { siblingKind: 'dynamic-not-built-in', threadId, turnId, requestId: request.id, providerRequests: providerSequence });
  const countBefore = providerSequence, waitedAt = performance.now();
  // Real linkedToolClientTimeout = 3*45s+10s+5s = 150s. Exactly one long wait.
  await delay(155000);
  assert.equal(providerSequence, countBefore, 'parent made a new model iteration while tool was pending');
  assert(!client.frames.some(frame => frame.method === 'turn/completed' && frame.params.turn.id === turnId), 'parent completed while tool was pending');
  assert(!client.isClosed, 'native reader/process died during pending tool');
  await client.call('thread/read', { threadId, includeTurns: false });
  check('pending-beyond-linked-mcp-timeout', { ordinaryMCPTimeoutMs: 150000, waitedMs: Math.round(performance.now() - waitedAt), providerRequests: providerSequence, readerResponsive: true, noCompletion: true });
  const finalPayload = 'fixture-batch-complete-unique-result';
  client.send({ id: request.id, result: { success: true, contentItems: [{ type: 'inputText', text: finalPayload }] } });
  const sibling = await event(client, frame => frame.method === 'item/tool/call' && frame.params.tool === 'fixture_sibling', 15000, 'same-response dynamic sibling after first result');
  assert.equal(providerSequence, countBefore, 'next model iteration before dynamic sibling settled');
  check('same-response-dynamic-sibling-dispatched', { siblingKind: 'dynamic-not-built-in', siblingRequestId: sibling.id, afterMainResult: true });
  client.send({ id: sibling.id, result: { success: true, contentItems: [{ type: 'inputText', text: 'fixture-sibling-complete' }] } });
  assert.equal((await finish(client, threadId, turnId)).status, 'completed');
  assert.equal(providerSequence, countBefore + 1);
  const continuation = JSON.parse(await readFile(join(root, `provider-${providerSequence}.json`), 'utf8'));
  assert(JSON.stringify(continuation.input).includes(finalPayload), 'consolidated output missing in continuation');
  check('one-result-one-continuation', { providerRequests: providerSequence });
  await endClient(client);
  client = await openClient('resume-born-with-tool');
  const resumed = await client.call('thread/resume', { threadId, cwd, model: 'fixture', modelProvider: 'klm_fixture', approvalPolicy: 'never', sandbox: 'read-only', excludeTurns: true });
  assert.equal(resumed.thread.id, threadId);
  plan = { kind: 'delegate' };
  const cancelledTurn = await begin(client, threadId, 'Fixture cancellation during a resumed dynamic request.');
  const cancelRequest = await event(client, frame => frame.method === 'item/tool/call' && frame.params.turnId === cancelledTurn, 15000, 'resumed dynamic tool');
  check('tool-persisted-on-same-thread-resume', { threadId, callId: cancelRequest.params.callId });
  const beforeCancel = providerSequence;
  await client.call('turn/interrupt', { threadId, turnId: cancelledTurn });
  assert.equal((await finish(client, threadId, cancelledTurn)).status, 'interrupted');
  await endClient(client);
  client = await openClient('resume-after-cancel');
  await client.call('thread/resume', { threadId, cwd, excludeTurns: true });
  // No turn/start is sent; one bounded observation catches automatic replay.
  await delay(1500);
  assert.equal(providerSequence, beforeCancel);
  assert(!client.frames.some(frame => frame.method === 'item/tool/call'));
  check('cancel-resume-no-automatic-reexecution', { providerRequests: providerSequence, threadId });
}
async function adoptionScenario() {
  client = await openClient('born-without-tool');
  const started = await client.call('thread/start', baseParams());
  const threadId = started.thread.id; report.threadId = threadId;
  const firstTurn = await begin(client, threadId, 'Fixture legacy thread without dynamic tools.');
  assert.equal((await finish(client, threadId, firstTurn)).status, 'completed');
  assert(!report.providerRequests.at(-1).tools.includes('subagent_run'));
  await endClient(client);
  client = await openClient('resume-without-tool');
  const resumed = await client.call('thread/resume', { threadId, cwd, excludeTurns: true });
  assert.equal(resumed.thread.id, threadId);
  const nextTurn = await begin(client, threadId, 'Fixture same native thread after supported resume.');
  assert.equal((await finish(client, threadId, nextTurn)).status, 'completed');
  assert(!report.providerRequests.at(-1).tools.includes('subagent_run'));
  check('legacy-thread-resume-keeps-no-dynamic-tool', { threadId, tools: report.providerRequests.at(-1).tools });
  // Supported schema inventory is generated from the installed executable in CP0.
  // Do not inject undocumented resume fields, edit transcript, fork, or replace ID.
  const schemaRoot = join(parent, 'delegation-cp0-codex-01534-schema');
  const resumeSchema = JSON.parse(await readFile(join(schemaRoot, 'v2', 'ThreadResumeParams.json'), 'utf8'));
  const requests = JSON.parse(await readFile(join(schemaRoot, 'ClientRequest.json'), 'utf8'));
  const dynamicConsumers = Object.entries(requests.definitions ?? {}).filter(([, def]) => JSON.stringify(def).includes('#/definitions/DynamicToolSpec')).map(([name]) => name);
  assert(!('dynamicTools' in resumeSchema.properties));
  assert.deepEqual(dynamicConsumers, ['ThreadStartParams']);
  report.blocker = { code: 'codex_existing_thread_tool_adoption_unavailable_in_exported_protocol', threadId, dynamicConsumers, resumeHasDynamicTools: false, transcriptEdited: false, forked: false, replacementCreated: false };
  check('supported-protocol-adoption-blocker', report.blocker);
}
let watchdog;
try {
  console.log(JSON.stringify({ root, scenario, fixtureURL: `http://127.0.0.1:${port}/v1`, deadlineMs: scenario === 'new-resume' ? 230000 : 60000 }));
  await timeout(scenario === 'new-resume' ? newResumeScenario() : adoptionScenario(), scenario === 'new-resume' ? 230000 : 60000, 'entire scenario');
  assert(!fixtureFatal, fixtureFatal?.message);
  report.outcome = report.blocker ? 'blocker' : 'passed';
} catch (error) { report.outcome = 'failed'; report.error = { message: error.message, stack: error.stack }; process.exitCode = 1; }
finally {
  lifetime.abort(new Error('scenario finished; no further dispatch'));
  // A stalled cleanup is also bounded; driver exits rather than hanging forever.
  watchdog = setTimeout(() => { report.outcome = 'cleanup_unconfirmed'; report.cleanupError = 'cleanup deadline'; writeFileSync(join(root, 'report.json'), JSON.stringify(report, null, 2)); console.error('cleanup deadline'); process.exit(2); }, 20000);
  for (const value of clients) { try { await endClient(value); } catch (error) { report.outcome = 'cleanup_unconfirmed'; report.cleanupError = error.message; process.exitCode = 1; } }
  server.closeAllConnections(); await new Promise(resolve => server.close(resolve));
  report.durationMs = elapsed(); report.endedAt = new Date().toISOString();
  await logWrites;
  await writeFile(join(root, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ outcome: report.outcome, durationMs: report.durationMs, error: report.error?.message, artifact: join(root, 'report.json'), cleanup: report.processes.map(entry => ({ pid: entry.pid, closedMs: entry.closedMs, pidAbsent: entry.pidAbsent })) }));
  clearTimeout(watchdog);
}
