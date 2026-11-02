import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const read = name => readFileSync(new URL(name, import.meta.url), 'utf8');
const moduleURL = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;
const stub = moduleURL(`
  export const delegationMetadata = { name: 'subagent_run', description: 'Delegate', schema: { type: 'object' } };
  export const delegationParentEligible = role => role === '';
  export const delegationLeafForbidden = name => /^(subagent_run|session_create|klm_linked_)/.test(name);
  export const createDelegationTransport = () => ({
    async ensureReady() {}, async gate() {}, assertCanContinue() {},
    async execute(request, id, signal) { return { id, aborted: signal.aborted }; },
  });
`);
const asset = read('delegation-opencode.mjs');
const configured = role => asset
  .replace("'./delegation-protocol.mjs'", JSON.stringify(stub))
  .replace('/*KLM_DELEGATION_CONFIG*/{}', JSON.stringify({ enabled: true, parentRole: role }));

for (const role of ['', 'subagent']) {
  const { setupV2, default: legacy } = await import(moduleURL(configured(role)));
  assert.equal(typeof legacy, 'function');
  const tools = new Map(['task', 'subagent', 'read', 'session_create', 'subagent_run', 'klm_linked_send'].map(id => [id, { id }]));
  const hooks = {};
  await setupV2({
    tool: {
      async transform(edit) { edit({ list: () => [...tools.values()], remove: id => tools.delete(id), add: tool => tools.set(tool.name, tool) }); },
      async hook(name, callback) { hooks[name] = callback; },
    },
    session: { async hook(name, callback) { hooks[name] = callback; } },
  });
  assert(tools.has('read'));
  assert(!tools.has('task') && !tools.has('subagent'));
  const event = { tools: { task: {}, subagent: {}, read: {}, subagent_run: {}, session_create: {} } };
  await hooks.context(event);
  assert(!('task' in event.tools) && !('subagent' in event.tools));
  await assert.rejects(hooks['execute.before']({ tool: 'task' }), /unavailable/);
  if (role === '') {
    const result = await tools.get('subagent_run').execute({}, { id: 'owned-call', signal: new AbortController().signal });
    assert.deepEqual(JSON.parse(result.content), { id: 'owned-call', aborted: false });
  } else {
    assert(!tools.has('subagent_run') && !tools.has('session_create') && !tools.has('klm_linked_send'));
    assert(!('subagent_run' in event.tools) && !('session_create' in event.tools));
  }
}

const policy = read('opencode-permissions-plugin.mjs');
for (const graph of ['async function graphPlugin() {}', read('opencode-graph-plugin.mjs')]) {
  for (const delegation of ['async function delegationPlugin() {}', "import { setupV2 as delegationPlugin } from './delegation-opencode.mjs';"]) {
    const generated = `${delegation}\n${graph}\n${policy}`;
    const checked = spawnSync(process.execPath, ['--input-type=module', '--check'], { input: generated, encoding: 'utf8' });
    assert.equal(checked.status, 0, checked.stderr);
    assert.equal([...generated.matchAll(/export\s+default/g)].length, 1);
  }
}
console.log('OpenCode V2 plugin composition and delegation checks passed.');
