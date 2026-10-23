// Exercise the installed Pi RPC and the actual owned extension without a model
// request. Everything, including Pi's agent directory, lives in a temporary root.
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

const pkg = resolve(process.argv[2]);
const source = await readFile(fileURLToPath(new URL('../pi-permissions.ts', import.meta.url)), 'utf8');
const root = await mkdtemp(join(tmpdir(), 'klm-pi-runtime-'));
const token = 'disposable-probe-only';
const server = createServer(async (req, res) => {
  assert.equal(req.headers.authorization, `Bearer ${token}`);
  let body = ''; for await (const chunk of req) body += chunk;
  const message = JSON.parse(body);
  assert.equal(message.method, 'tools/list');
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({ jsonrpc: '2.0', id: message.id, result: { tools: [] } }));
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
try {
  const cwd = join(root, 'project'), agentDir = join(root, 'agent');
  for (const dir of [cwd, agentDir, join(agentDir, 'skills/forbidden'), join(cwd, '.pi/skills/forbidden-project')]) await mkdir(dir, { recursive: true });
  await writeFile(join(agentDir, 'skills/forbidden/SKILL.md'), '---\nname: forbidden\ndescription: fixture\n---\n');
  await writeFile(join(cwd, '.pi/skills/forbidden-project/SKILL.md'), '---\nname: forbidden-project\ndescription: fixture\n---\n');
  const packageDir = join(root, 'configured-package');
  await mkdir(join(packageDir, 'forbidden-package'), { recursive: true });
  await writeFile(join(packageDir, 'package.json'), JSON.stringify({ name: 'local-probe', version: '0.0.0', pi: { extensions: ['./forbidden.ts'], skills: ['./forbidden-package'] } }));
  await writeFile(join(packageDir, 'forbidden.ts'), 'throw new Error("Configured package extension must not load");');
  await writeFile(join(packageDir, 'forbidden-package/SKILL.md'), '---\nname: forbidden-package\ndescription: fixture\n---\n');
  await writeFile(join(agentDir, 'settings.json'), JSON.stringify({ packages: [packageDir, 'npm:klm-nonexistent-offline-probe@0.0.0'] }));
  const selected = join(root, 'snapshots/revision/selected');
  await mkdir(selected, { recursive: true });
  await writeFile(join(selected, 'SKILL.md'), '---\nname: selected\ndescription: fixture\n---\n');
  const config = join(root, 'catalog.json'), extension = join(root, 'owned.ts');
  await writeFile(config, JSON.stringify({ url: `http://127.0.0.1:${server.address().port}/mcp`, token, tools: [], toolTimeoutMs: 20000 }));
  await writeFile(extension, source.replace('/*KLM_LINKED_CONFIG*/{}', `JSON.parse(readFileSync(${JSON.stringify(config)}, 'utf8'))`));
  for (const paths of [[], [selected]]) {
    const child = spawn(process.execPath, [join(pkg, 'dist/cli.js'), '--mode', 'rpc', '--offline', '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes', '--extension', extension, '--session', join(root, `session-${paths.length}.jsonl`), ...paths.flatMap(path => ['--skill', path])], {
      cwd, env: { ...process.env, PI_CODING_AGENT_DIR: agentDir }, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true,
    });
    const lines = createInterface({ input: child.stdout });
    let stderr = ''; child.stderr.on('data', chunk => { stderr += chunk; });
    try {
      await new Promise((resolve, reject) => {
        let ready = false, inventory = false;
        const observed = [];
        const timer = setTimeout(() => reject(new Error(`Pi startup deadline: ${stderr.slice(0, 1000)} ${JSON.stringify(observed)}`)), 20000);
        const finish = () => { if (ready && inventory) { clearTimeout(timer); resolve(); } };
        child.on('error', reject);
        child.on('exit', code => { if (!ready || !inventory) { clearTimeout(timer); reject(new Error(`Pi exited (${code}): ${stderr.slice(0, 1000)}`)); } });
        lines.on('line', line => {
          try {
            const frame = JSON.parse(line);
            observed.push({ type: frame.type, id: frame.id, method: frame.method, message: frame.message });
            if (frame.type === 'extension_ui_request' && frame.message === 'klm.permissions.ready.v1') { ready = true; finish(); }
            if (frame.type === 'response' && frame.id === 'inventory') {
              assert.equal(frame.success, true);
              assert(frame.data.commands.every(command => command.source === 'skill' || command.name === 'llama' && command.sourceInfo?.path === '<inline:llama.cpp>'));
              assert.deepEqual(frame.data.commands.filter(command => command.source === 'skill').map(command => command.name), paths.length ? ['skill:selected'] : []);
              inventory = true; finish();
            }
            if (frame.type === 'extension_error') throw new Error('Owned extension failed.');
          } catch (error) { clearTimeout(timer); reject(error); }
        });
        child.stdin.write(JSON.stringify({ type: 'get_commands', id: 'inventory' }) + '\n');
      });
    } finally { lines.close(); child.kill(); await new Promise(resolve => child.once('close', resolve)); }
  }
  console.log('Pi RPC probe passed: owned extension handshake and exact empty/selected catalogs. No model calls or real credentials/library writes.');
} finally { await new Promise(resolve => server.close(resolve)); await rm(root, { recursive: true, force: true }); }
