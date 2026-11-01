// Hermetic regression: unsafe settlement must keep the local parent gate closed
// even before/as native abort completes. No network, harness or engine is started.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createDelegationTransport } from '../../delegation-protocol.mjs';

const originalFetch = globalThis.fetch;
let cancelled = 0;
globalThis.fetch = async (_url, options) => {
  const request = JSON.parse(options.body);
  let result;
  switch (request.method) {
    case 'klm/delegation/ready':
      result = { version: 1, maxChildren: 8, maxConcurrency: 4, activeBatchLimit: 1 };
      break;
    case 'klm/delegation/admit':
      result = { version: 1, accepted: true, batchId: 'fixture-batch', operationId: 'fixture-operation', childCount: 1, maxConcurrency: 4 };
      break;
    case 'klm/delegation/wait':
      result = { revision: '1', state: 'stop', stopRequired: true };
      break;
    case 'klm/delegation/cancel':
      cancelled++;
      result = { accepted: true };
      break;
    case 'klm/delegation/gate':
      result = { allowed: true };
      break;
    default: throw new Error('Unexpected fixture RPC');
  }
  return new Response(JSON.stringify({ jsonrpc: '2.0', id: request.id, result }), { status: 200 });
};
try {
  const transport = createDelegationTransport({ parentRole: '', bridgeUrl: 'http://127.0.0.1:1/fixture', bridgeToken: 'fixture-only' });
  const signal = new AbortController().signal;
  await assert.rejects(transport.execute({ operationId: 'fixture-operation', children: [{ key: 'one', task: 'Fixture' }] }, 'native-call', signal));
  assert.equal(cancelled, 1);
  await assert.rejects(transport.gate('bash', signal), 'Fatal delegation must remain locally gated until owning runtime stops.');
  assert.equal(transport.isStopRequired(), true);
  assert.equal(transport.canContinue(), false);
  assert.throws(() => transport.assertCanContinue());
  await assert.rejects(transport.execute({ operationId: 'later', children: [{ key: 'one', task: 'Later' }] }, 'later-call', signal));
  console.log('Fatal delegation retains its stop gate.');
} finally {
  globalThis.fetch = originalFetch;
}

// Additional focal cases preserve the manager's regression above. These are
// state-machine/mock-hook tests, not native harness timing or product handshakes.
const request = { operationId: 'fixture-operation', children: [{ key: 'one', task: 'Fixture' }] };
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
async function bounded(promise) {
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('Hermetic scenario deadline')), 2000); })]); }
  finally { clearTimeout(timer); }
}
function success(batchId = 'fixture-batch', operationId = request.operationId) {
  return { revision: '2', state: 'settled', result: { version: 1, batchId, operationId, outcome: 'completed', isError: false, canContinue: true, children: [{ key: 'one', invocationId: 'invocation', childSessionId: 'child', outcome: 'completed', finality: { nativeSettled: true, processesDrained: true, toolCallsSettled: true } }] } };
}
function fixture(handler) {
  const counts = { admit: 0, cancel: 0, gate: 0, work: 0 };
  const operations = new Set();
  const fetch = async (_url, options) => {
    const call = JSON.parse(options.body), method = call.method;
    if (method === 'klm/delegation/admit') counts.admit++;
    if (method === 'klm/delegation/cancel') counts.cancel++;
    if (method === 'klm/delegation/gate') counts.gate++;
    const acceptWork = () => {
      if (method === 'klm/delegation/admit' && !operations.has(call.params.request.operationId)) { operations.add(call.params.request.operationId); counts.work++; }
    };
    let custom;
    try { custom = await handler?.(call, options, counts); }
    catch (error) { acceptWork(); throw error; } // Explicit lost-after-acceptance scenarios.
    if (custom?.error) return new Response(JSON.stringify({ jsonrpc: '2.0', id: call.id, error: custom.error }));
    acceptWork();
    const result = custom?.result ?? (method === 'klm/delegation/ready'
      ? { version: 1, specDigest: call.params.specDigest, maxChildren: 8, maxConcurrency: 4, activeBatchLimit: 1 }
      : method === 'klm/delegation/admit'
        ? { version: 1, accepted: true, batchId: 'fixture-batch', operationId: call.params.request.operationId, childCount: 1, maxConcurrency: 4 }
        : method === 'klm/delegation/wait' ? success() : method === 'klm/delegation/gate' ? { allowed: true } : { cancelled: true });
    return new Response(JSON.stringify({ jsonrpc: '2.0', id: call.id, result }));
  };
  return { counts, fetch };
}
const config = { enabled: true, parentRole: '', bridgeUrl: 'http://127.0.0.1:1/fixture', bridgeToken: 'fixture-only' };
const rejected = { code: -32000, message: 'Rejected before admission', data: { accepted: false, code: 'configuration_blocked' } };

async function recoverableRejection() {
  let rejectNext = true;
  const mock = fixture(call => {
    if (call.method === 'klm/delegation/admit' && rejectNext) { rejectNext = false; return { error: rejected }; }
  });
  globalThis.fetch = mock.fetch;
  try {
    const transport = createDelegationTransport(config), signal = new AbortController().signal;
    await assert.rejects(transport.execute({ operationId: 'invalid', children: [] }, 'input-invalid', signal), error => error.delegationInputRejected === true);
    assert.equal(mock.counts.admit, 0);
    await assert.rejects(transport.execute(request, 'input-rejected', signal), error => error.delegationInputRejected === true);
    assert.equal(transport.isStopRequired(), false);
    assert.equal(transport.canContinue(), true);
    assert.equal(mock.counts.cancel, 0);
    assert.equal(mock.counts.work, 0);
    await transport.gate('bash', signal);
    assert.equal((await transport.execute(request, 'valid-retry', signal)).children.length, 1);
    assert.equal(transport.canContinue(), true);
  } finally { globalThis.fetch = originalFetch; }
}

async function aliasCase(mode) {
  const waiting = deferred(), finish = deferred(), aliasDispatched = deferred();
  const mock = fixture(async (call, options) => {
    if (call.method === 'klm/delegation/wait') { waiting.resolve(); return { result: await finish.promise }; }
    if (call.method === 'klm/delegation/admit' && call.params.nativeCallId === 'alias') {
      aliasDispatched.resolve();
      if (mode === 'rejected') return { error: rejected };
      if (mode === 'uncertain') throw new TypeError('Alias accepted but its response was lost');
      return new Promise((_, reject) => {
        const onAbort = () => reject(options.signal.reason ?? new Error('Alias cancelled'));
        if (options.signal.aborted) onAbort(); else options.signal.addEventListener('abort', onAbort, { once: true });
      });
    }
  });
  globalThis.fetch = mock.fetch;
  const signal = new AbortController().signal, aliasSignal = new AbortController();
  const transport = createDelegationTransport(config);
  const first = transport.execute(request, 'original', signal).then(value => ({ value }), error => ({ error }));
  try {
    await bounded(waiting.promise);
    await assert.rejects(transport.execute({ operationId: 'invalid', children: [] }, 'bad-input', signal), error => error.delegationInputRejected === true);
    await assert.rejects(transport.execute({ ...request, operationId: 'independent' }, 'other', signal), error => error.delegationInputRejected === true);
    assert.equal(transport.isStopRequired(), false);
    assert.equal(mock.counts.admit, 1);
    const alias = assert.rejects(transport.execute(request, 'alias', aliasSignal.signal), error => mode === 'rejected' ? error.delegationInputRejected === true : error.delegationStopRequired === true);
    await bounded(aliasDispatched.promise);
    if (mode === 'cancelled') aliasSignal.abort(new Error('Native alias cancelled after dispatch'));
    await bounded(alias);
    if (mode === 'rejected') {
      assert.equal(transport.isStopRequired(), false);
      assert.equal(mock.counts.cancel, 0);
    } else {
      assert.equal(transport.isStopRequired(), true);
      assert.equal(transport.canContinue(), false);
      assert.throws(() => transport.assertCanContinue());
      await assert.rejects(transport.gate('bash', signal));
      assert.equal(mock.counts.cancel, 1);
    }
    // Mock wait deliberately ignores the abort signal, returning a late success.
    // A fatal alias must prevent that result from releasing the original call.
    finish.resolve(success());
    const completed = await bounded(first);
    if (mode === 'rejected') {
      assert(completed.value); assert.equal(transport.canContinue(), true);
    } else {
      assert.equal(completed.value, undefined);
      assert.equal(completed.error.delegationStopRequired, true);
      assert.equal(transport.isStopRequired(), true);
      await assert.rejects(transport.gate('bash', signal));
      assert.equal(mock.counts.cancel, 1);
    }
    assert.equal(mock.counts.admit, 2);
    assert.equal(mock.counts.work, 1, 'Alias/cancellation must not create a second execution');
  } finally {
    finish.resolve(success()); aliasSignal.abort(); await bounded(first);
    globalThis.fetch = originalFetch;
  }
}

async function postAdmissionFailure(kind) {
  const mock = fixture(call => {
    if (call.method !== 'klm/delegation/wait') return;
    if (kind === 'transport') throw new TypeError('Accepted batch lost its wait response');
    return { error: rejected }; // A wait error cannot masquerade as pre-admission rejection.
  });
  globalThis.fetch = mock.fetch;
  try {
    const transport = createDelegationTransport(config), signal = new AbortController().signal;
    await assert.rejects(transport.execute(request, 'post-admission', signal), error => error.delegationStopRequired === true && error.delegationInputRejected !== true);
    assert.equal(transport.isStopRequired(), true);
    assert.equal(transport.canContinue(), false);
    assert.throws(() => transport.assertCanContinue());
    await assert.rejects(transport.gate('bash', signal));
    assert.equal(mock.counts.cancel, 1);
  } finally { globalThis.fetch = originalFetch; }
}

async function aliasPendingContinuation() {
  const waiting = deferred(), finish = deferred(), aliasDispatched = deferred(), deliverAlias = deferred();
  const mock = fixture(async call => {
    if (call.method === 'klm/delegation/wait') { waiting.resolve(); return { result: await finish.promise }; }
    if (call.method === 'klm/delegation/admit' && call.params.nativeCallId === 'pending-alias') { aliasDispatched.resolve(); await deliverAlias.promise; }
  });
  globalThis.fetch = mock.fetch;
  const transport = createDelegationTransport(config), signal = new AbortController().signal;
  const first = transport.execute(request, 'original', signal);
  let alias;
  try {
    await bounded(waiting.promise);
    alias = transport.execute(request, 'pending-alias', signal);
    await bounded(aliasDispatched.promise);
    finish.resolve(success()); await bounded(first);
    assert.equal(transport.isWaiting(), true, 'An in-flight alias still owns the local barrier');
    assert.equal(transport.canContinue(), false);
    assert.throws(() => transport.assertCanContinue());
    await assert.rejects(transport.gate('bash', signal));
    deliverAlias.resolve(); await bounded(alias);
    assert.equal(transport.canContinue(), true);
    assert.equal(mock.counts.work, 1);
    assert.equal(mock.counts.cancel, 0);
  } finally { finish.resolve(success()); deliverAlias.resolve(); await bounded(Promise.allSettled([first, alias])); globalThis.fetch = originalFetch; }
}

async function admissionUncertainty() {
  const mock = fixture(call => { if (call.method === 'klm/delegation/admit') throw new TypeError('Admission sent; acceptance response lost'); });
  globalThis.fetch = mock.fetch;
  try {
    const transport = createDelegationTransport(config), signal = new AbortController().signal;
    await assert.rejects(transport.execute(request, 'uncertain-admission', signal), error => error.delegationStopRequired === true);
    assert.equal(transport.isStopRequired(), true);
    assert.throws(() => transport.assertCanContinue());
    await assert.rejects(transport.gate('bash', signal));
    await assert.rejects(transport.execute(request, 'must-not-retry', signal));
    assert.equal(mock.counts.admit, 1);
    assert.equal(mock.counts.work, 1);
    assert.equal(mock.counts.cancel, 0, 'Unknown batch ID is not invented; owner Stop retains authority');
  } finally { globalThis.fetch = originalFetch; }
}

async function mockHookCase(harness) {
  const protocolSource = await readFile(new URL('../../delegation-protocol.mjs', import.meta.url), 'utf8');
  const fixtureMetadata = { name: 'subagent_run', description: 'Hermetic hook fixture', schema: { type: 'object', properties: {} }, specDigest: 'fixture-only-not-a-product-manifest' };
  const protocolURL = 'data:text/javascript;base64,' + Buffer.from(protocolSource.replace('/*KLM_DELEGATION_METADATA*/{}', JSON.stringify(fixtureMetadata))).toString('base64');
  const source = (await readFile(new URL(`../../delegation-${harness}.mjs`, import.meta.url), 'utf8'))
    .replace("'./delegation-protocol.mjs'", JSON.stringify(protocolURL)).replace('/*KLM_DELEGATION_CONFIG*/{}', JSON.stringify(config));
  const module = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
  const mock = fixture(call => call.method === 'klm/delegation/wait' ? { result: { revision: '1', state: 'stop', stopRequired: true } } : undefined);
  globalThis.fetch = mock.fetch;
  const signal = new AbortController().signal;
  let aborts = 0;
  const pendingAbort = deferred();
  try {
    if (harness === 'pi') {
      const hooks = new Map(); let tool;
      module.default({ registerTool(value) { tool = value; }, on(name, hook) { hooks.set(name, hook); } });
      const context = { signal, abort() { aborts++; } };
      await assert.rejects(tool.execute('original', request, signal, undefined, context));
      await assert.rejects(hooks.get('before_provider_request')({}, context));
      await assert.rejects(hooks.get('before_agent_start')({}, context));
      assert.equal((await hooks.get('tool_call')({ toolName: 'bash' }, context)).block, true);
    } else {
      const hooks = await module.default({ client: { session: { abort() { aborts++; return pendingAbort.promise; } } } });
      await hooks['tool.execute.before']({ tool: 'subagent_run', sessionID: 'parent', callID: 'original' }, { args: request });
      await assert.rejects(hooks.tool.subagent_run.execute(request, { sessionID: 'parent', abort: signal }));
      assert.equal(aborts, 1); // Native abort HTTP is STILL pending here.
      await assert.rejects(hooks['experimental.chat.messages.transform']({}, { messages: [] }));
      await assert.rejects(hooks['tool.execute.before']({ tool: 'bash', sessionID: 'parent', callID: 'later' }, { args: {} }));
    }
    assert(aborts > 0);
    assert.equal(mock.counts.cancel, 1);
  } finally { pendingAbort.resolve(); globalThis.fetch = originalFetch; }
}

await bounded(recoverableRejection());
await bounded(aliasCase('rejected'));
await bounded(aliasCase('uncertain'));
await bounded(aliasCase('cancelled'));
await bounded(admissionUncertainty());
await bounded(postAdmissionFailure('transport'));
await bounded(postAdmissionFailure('rejection-marker'));
await bounded(aliasPendingContinuation());
await bounded(mockHookCase('pi'));
await bounded(mockHookCase('opencode'));
console.log('B-R1 focal cases passed: persistent fatal/continuation latch, recoverable rejection, alias cancellation/uncertainty and both mock harness hooks.');
