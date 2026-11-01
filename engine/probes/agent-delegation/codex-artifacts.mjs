// Offline attestation of already-produced disposable fixture artifacts. No harness starts.
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
const root = resolve(process.argv[2]);
const load = async name => JSON.parse(await readFile(join(root, name), 'utf8'));
const report = await load('report.json');
assert.equal(report.scenario, 'new-resume'); assert.equal(report.outcome, 'passed');
const first = await load('provider-1.json'), continued = await load('provider-2.json'), resumed = await load('provider-3.json');
const spec = body => body.tools.find(tool => tool.name === 'subagent_run');
assert(spec(first)); assert.deepEqual(spec(resumed), spec(first));
assert.deepEqual(spec(first).parameters, { type: 'object', properties: { task: { type: 'string' } }, required: ['task'], additionalProperties: false });
const outputs = continued.input.filter(item => item.type === 'function_call_output' && JSON.stringify(item.output).includes('fixture-batch-complete-unique-result'));
assert.equal(outputs.length, 1, 'one consolidated tool output required');
assert(report.processes.every(item => item.pidAbsent && item.code === 0));
const result = { nativeThreadId: report.threadId, specSHA256: createHash('sha256').update(JSON.stringify(spec(first))).digest('hex'), identicalSpecAfterProcessRestart: true, requiredSchemaMatched: true, consolidatedOutputCount: outputs.length, nativeProcessesClosed: true };
await writeFile(join(root, 'spec-attestation.json'), JSON.stringify(result, null, 2));
console.log(JSON.stringify(result, null, 2));
