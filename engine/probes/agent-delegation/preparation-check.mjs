// Hermetic owner-bound preparation transport checks; no sockets/harness/model.
import assert from 'node:assert/strict';
import { createDelegationTransport } from '../../delegation-protocol.mjs';
const original = globalThis.fetch;
const request = { operationId: 'operation', children: [{ key: 'worker', task: 'Fixture' }] };
const config = { parentRole: '', bridgeUrl: 'http://127.0.0.1:1/fixture', bridgeToken: 'fixture-only' };
const receipt = { version: 1, accepted: true, batchId: 'batch', operationId: 'operation', childCount: 1, maxConcurrency: 4 };
async function scenario(mode) {
  const counts = { admit: 0, prepared: 0, cancel: 0 };
  const controller = new AbortController();
  globalThis.fetch = async (_url, options) => {
    const call = JSON.parse(options.body); let result, error;
    switch (call.method) {
      case 'klm/delegation/ready': result = { version: 1, specDigest: call.params.specDigest, maxChildren: 8, maxConcurrency: 4, activeBatchLimit: 1 }; break;
      case 'klm/delegation/admit': counts.admit++; result = { version: 1, preparing: true, preparationId: 'preparation' }; break;
      case 'klm/delegation/prepared':
        counts.prepared++; assert(counts.prepared <= 3, 'No unbounded fixture preparation loop');
        if (mode === 'transport') throw new TypeError('Preparation acceptance response lost');
        if (['rejected', 'wrong-id', 'wrong-phase'].includes(mode)) error = { code: -32000, message: 'Preparation failed', data: { accepted: false, code: 'configuration_blocked', phase: mode === 'wrong-phase' ? 'post_admission' : 'pre_admission', preparationId: mode === 'wrong-id' ? 'different' : 'preparation' } };
        else if (mode === 'changed') result = { version: 1, preparing: true, preparationId: 'different' };
        else if (mode === 'mixed') result = { version: 1, preparing: true, preparationId: 'preparation', receipt };
        else result = counts.prepared === 1 ? { version: 1, preparing: true, preparationId: 'preparation' } : { version: 1, preparing: false, preparationId: 'preparation', receipt };
        break;
      case 'klm/delegation/wait':
        if (mode === 'wait-rejection') error = { code: -32000, message: 'Wait failed', data: { accepted: false, code: 'configuration_blocked', phase: 'pre_admission', preparationId: 'preparation' } };
        else result = { state: 'settled', revision: '1', result: { version: 1, batchId: 'batch', operationId: 'operation', outcome: 'completed', isError: false, canContinue: true, children: [{ key: 'worker', childSessionId: 'child', invocationId: 'invocation', outcome: 'completed', finality: { nativeSettled: true, processesDrained: true, toolCallsSettled: true } }] } };
        break;
      case 'klm/delegation/cancel': counts.cancel++; result = { cancelled: true }; break;
      case 'klm/delegation/gate': result = { allowed: true }; break;
      default: throw new Error('Unexpected fixture RPC');
    }
    return new Response(JSON.stringify({ jsonrpc: '2.0', id: call.id, ...(error ? { error } : { result }) }));
  };
  try {
    const transport = createDelegationTransport(config);
    if (mode === 'success') {
      assert.equal((await transport.execute(request, 'native', controller.signal)).children.length, 1);
      assert.equal(transport.canContinue(), true); assert.equal(counts.prepared, 2); assert.equal(counts.cancel, 0);
    } else if (mode === 'rejected') {
      await assert.rejects(transport.execute(request, 'native', controller.signal), error => error.delegationInputRejected === true);
      assert.equal(transport.isStopRequired(), false); assert.equal(transport.canContinue(), true); assert.equal(counts.cancel, 0);
    } else {
      await assert.rejects(transport.execute(request, 'native', controller.signal), error => error.delegationStopRequired === true);
      assert.equal(transport.canContinue(), false);
      await assert.rejects(transport.gate('bash', controller.signal));
      await assert.rejects(transport.execute(request, 'no-admission-retry', controller.signal));
      assert.equal(counts.cancel, mode === 'wait-rejection' ? 1 : 0);
    }
    assert.equal(counts.admit, 1, 'Preparation continuation must not resend admission');
  } finally { controller.abort(); globalThis.fetch = original; }
}
for (const mode of ['success', 'rejected', 'transport', 'wrong-id', 'wrong-phase', 'changed', 'mixed', 'wait-rejection']) {
  let timer;
  try { await Promise.race([scenario(mode), new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`Scenario deadline: ${mode}`)), 2000); })]); }
  finally { clearTimeout(timer); }
}
console.log('Preparation transport passed: bounded continuation, no admission resend, explicit rejection proof and fatal uncertainty.');
