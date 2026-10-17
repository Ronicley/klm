import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

// No native process/model: exercise the owned boundary hook with delayed part
// persistence and verify that neither the snapshot nor recovery ACK runs early.
const source = await readFile(new URL('./opencode-permissions-plugin.mjs', import.meta.url), 'utf8');
const module = await import('data:text/javascript;base64,' + Buffer.from(
  'async function graphPlugin() { return {}; }\n' + source,
).toString('base64'));

test('steering boundary waits for complete parts and updates this snapshot', async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  const parts = [{ type: 'text', text: 'Message 1: A\nMessage 2: B' }];
  globalThis.fetch = async (_url, options) => {
    const { method } = JSON.parse(options.body);
    calls.push(method);
    return { ok: true, json: async () => ({ result: method.endsWith('/take') ? { batchID: 'a', parts } : { consumed: true } }) };
  };
  try {
    let persist;
    const context = { client: { session: { prompt: async options => {
      assert.equal(options.body.noReply, true);
      assert.equal(options.path.id, 'parent');
      assert.deepEqual(options.body.parts, parts);
      return await new Promise(resolve => { persist = resolve; });
    } } } };
    const hooks = await module.default(context);
    const output = { messages: [{ info: { role: 'user', id: 'initial', sessionID: 'parent', model: {} }, parts: [] }] };
    const pending = hooks['experimental.chat.messages.transform']({}, output);
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(output.messages.length, 1);
    assert.deepEqual(calls, ['klm/steering/take']);
    const saved = { info: { role: 'user', id: 'native', sessionID: 'parent' }, parts: parts.map(part => ({ ...part, id: 'part1', sessionID: 'parent', messageID: 'native' })) };
    persist({ data: saved });
    await pending;
    assert.equal(output.messages[1], saved);
    assert.deepEqual(calls, ['klm/steering/take', 'klm/steering/consumed']);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('header-only native response never confirms consumption', async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (_url, options) => {
    const { method } = JSON.parse(options.body);
    calls.push(method);
    return { ok: true, json: async () => ({ result: { batchID: 'a', parts: [{ type: 'text', text: 'A' }] } }) };
  };
  try {
    const hooks = await module.default({ client: { session: { prompt: async () => ({ data: { info: { role: 'user', id: 'native', sessionID: 'parent' }, parts: [] } }) } } });
    const output = { messages: [{ info: { role: 'user', sessionID: 'parent', model: {} }, parts: [] }] };
    await assert.rejects(hooks['experimental.chat.messages.transform']({}, output), /complete steering input/);
    assert.equal(output.messages.length, 1);
    assert.deepEqual(calls, ['klm/steering/take']);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
