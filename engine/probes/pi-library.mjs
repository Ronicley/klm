// No model calls, credentials or real library writes. Usage: node pi-library.mjs <installed-package-dir>
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const pkg = resolve(process.argv[2]);
const { DefaultResourceLoader } = await import(pathToFileURL(join(pkg, 'dist/core/resource-loader.js')));
const { SettingsManager } = await import(pathToFileURL(join(pkg, 'dist/core/settings-manager.js')));
const root = await mkdtemp(join(tmpdir(), 'klm-pi-library-'));
try {
  const cwd = join(root, 'project'), agentDir = join(root, 'agent');
  const selected = join(root, 'selected');
  for (const [dir, name] of [[join(agentDir, 'skills/global'), 'global'], [join(cwd, '.pi/skills/project'), 'project'], [selected, 'selected']]) {
    await mkdir(dir, { recursive: true });
    await writeFile(join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: harmless fixture\n---\nFixture\n`);
  }
  await mkdir(join(agentDir, 'extensions'), { recursive: true });
  await writeFile(join(agentDir, 'extensions/forbidden.ts'), 'throw new Error("Unselected extension loaded");');
  const settingsManager = SettingsManager.inMemory({});
  for (const paths of [[], [selected]]) {
    const loader = new DefaultResourceLoader({ cwd, agentDir, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, additionalSkillPaths: paths });
    await loader.reload();
    assert.deepEqual(loader.getSkills().skills.map(s => s.name), paths.length ? ['selected'] : []);
    assert.equal(loader.getExtensions().extensions.length, 0);
    assert.equal(loader.getExtensions().errors.length, 0);
  }
  console.log('Pi resource-loader probe passed: empty/selected skill catalogs and excluded global extension. No model calls.');
} finally { await rm(root, { recursive: true, force: true }); }
