// Hermetic checks of the rendered versioned transport. No harness/model starts.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { writeFile, readFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = resolve(process.argv[2]);
let moduleURL = pathToFileURL(join(root, 'delegation-protocol.mjs')).href;
if (process.argv[3]) {
  // Reuse the frozen Go-rendered metadata when a concurrently edited unrelated
  // package file blocks another package build. Exercise the CURRENT source,
  // without changing the already-produced fixture/native history artifacts.
  const rendered = await readFile(join(root, 'delegation-protocol.mjs'), 'utf8');
  const metadata = JSON.parse(rendered.match(/export const delegationMetadata = (\{.*\});/)[1]);
  const source = await readFile(resolve(process.argv[3]), 'utf8');
  assert(source.includes('/*KLM_DELEGATION_METADATA*/{}'));
  moduleURL = 'data:text/javascript;base64,' + Buffer.from(source.replace('/*KLM_DELEGATION_METADATA*/{}', JSON.stringify(metadata))).toString('base64');
}
const { createDelegationTransport, delegationMetadata } = await import(moduleURL);
const controller = new AbortController(), batches = new Map();
let next = 0, cancelled = 0, firstAdmitted, release, waitArrived, gateAllowed = true;
const admitted = new Promise(resolve => { firstAdmitted = resolve; });
const waitObserved = new Promise(resolve => { waitArrived = resolve; });
const deadline = setTimeout(() => controller.abort(new Error('protocol fixture deadline')), 10000);
const server = createServer(async (req, res) => {
  let raw = ''; for await (const part of req) raw += part;
  const { id, method, params } = JSON.parse(raw);
  const reply = result => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ jsonrpc: '2.0', id, result })); };
  if (method === 'klm/delegation/ready') return reply({ version: 1, specDigest: delegationMetadata.specDigest, maxChildren: 8, maxConcurrency: 4, activeBatchLimit: 1 });
  if (method === 'klm/delegation/gate') return reply({ allowed: gateAllowed });
  if (method === 'klm/delegation/admit') {
    let batch = batches.get(params.request.operationId);
    if (!batch) { batch = { id: `batch-${++next}`, request: params.request }; batches.set(params.request.operationId, batch); }
    reply({ version: 1, accepted: true, batchId: batch.id, operationId: params.request.operationId, childCount: params.request.children.length, maxConcurrency: 4 });
    firstAdmitted(); return;
  }
  if (method === 'klm/delegation/cancel') { cancelled++; return reply({ cancelled: true }); }
  assert.equal(method, 'klm/delegation/wait');
  const batch = [...batches.values()].find(item => item.id === params.batchId); assert(batch);
  const finish = () => reply({ revision: '1', state: 'settled', result: { version: 1, batchId: batch.id, operationId: batch.request.operationId, outcome: batch.request.operationId === 'unsafe' ? 'finality_unconfirmed' : 'completed', isError: batch.request.operationId === 'unsafe', canContinue: batch.request.operationId !== 'unsafe', children: batch.request.children.map((child, i) => ({ key: child.key, childSessionId: `child-${i}`, invocationId: `inv-${i}`, outcome: 'completed', finality: { nativeSettled: true, processesDrained: true, toolCallsSettled: true } })) } });
  if (batch.request.operationId !== 'first' || batch.released) finish();
  else { const timer = setTimeout(finish, 1000); release = () => { clearTimeout(timer); batch.released = true; finish(); }; waitArrived(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const transport = createDelegationTransport({ parentRole: '', bridgeUrl: `http://127.0.0.1:${server.address().port}/mcp`, bridgeToken: 'fixture-only' });
const request = { operationId: 'first', children: [{ key: 'a', task: 'One' }, { key: 'b', task: 'Two' }] };
let outcome = 'failed';
try {
  const first = transport.execute(request, 'call-1', controller.signal);
  await Promise.race([admitted, new Promise((_, reject) => controller.signal.addEventListener('abort', () => reject(controller.signal.reason), { once: true }))]);
  await assert.rejects(transport.execute({ ...request, operationId: 'independent' }, 'call-2', controller.signal), error => error.delegationInputRejected === true);
  await assert.rejects(transport.gate('bash', controller.signal));
  // The first native operation remains pending, not cancelled by the second call.
  await Promise.race([waitObserved, new Promise((_, reject) => controller.signal.addEventListener('abort', () => reject(controller.signal.reason), { once: true }))]);
  release(); const result = await first; assert.equal(result.children.length, 2); assert.equal(cancelled, 0);
  await transport.execute(request, 'call-retry', controller.signal); assert.equal(batches.size, 1);
  gateAllowed = false; await assert.rejects(transport.gate('bash', controller.signal));
  await assert.rejects(transport.execute({ ...request, operationId: 'unsafe' }, 'call-unsafe', controller.signal), error => error.delegationInputRejected !== true);
  assert.equal(cancelled, 1);
  outcome = 'passed';
  console.log(JSON.stringify({ outcome, specDigest: delegationMetadata.specDigest, oneActiveBatch: true, retryNoDuplicateWork: true, deniedGateHonored: true, uncertainResultCancelled: true }));
} finally {
  controller.abort(); clearTimeout(deadline); server.closeAllConnections(); await new Promise(resolve => server.close(resolve));
  await writeFile(join(root, 'protocol-check.json'), JSON.stringify({ outcome, specDigest: delegationMetadata.specDigest, admittedBatches: batches.size, cancelled }));
}
