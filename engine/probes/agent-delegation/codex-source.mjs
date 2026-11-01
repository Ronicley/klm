// Read-only public-source investigation tied to the installed release tag.
import { mkdtemp, stat, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
const parent = resolve(process.argv[2]);
if (!(await stat(parent)).isDirectory()) throw new Error('temporary parent missing');
const root = await mkdtemp(join(parent, 'delegation-codex-source-'));
const commit = '3d2ee51ca2d5db578f328aa75e20aa22c0197c9a'; // rust-v0.153.4 dereferenced tag
const paths = process.argv[3] === 'session' ? [
  'codex-rs/core/src/session/session.rs',
] : [
  'codex-rs/app-server/src/request_processors/thread_processor.rs',
  'codex-rs/config/src/config_toml.rs',
  'codex-rs/core/src/config/mod.rs',
  'codex-rs/thread-store/src/types.rs',
  'codex-rs/core/src/thread_manager.rs',
];
const manifest = [];
for (const [index, path] of paths.entries()) {
  const url = `https://raw.githubusercontent.com/openai/codex/${commit}/${path}`;
  const response = await fetch(url, { signal: AbortSignal.timeout(15000) });
  if (!response.ok) throw new Error(`${response.status} ${path}`);
  const text = await response.text();
  if (text.length > 1024 * 1024) throw new Error('source bound exceeded');
  const file = `${index}-${path.split('/').at(-1)}`;
  await writeFile(join(root, file), text);
  const matches = text.split('\n').flatMap((line, n) => /dynamic_tools|dynamicTools|DynamicTool|resume_thread|ExtraConfig|extra_config/.test(line) ? [{ line: n + 1, text: line.trim().slice(0, 250) }] : []);
  manifest.push({ path, file, url, sha256: createHash('sha256').update(text).digest('hex'), matches });
}
await writeFile(join(root, 'manifest.json'), JSON.stringify({ commit, manifest }, null, 2));
console.log(JSON.stringify({ root, commit, manifest }, null, 2));
