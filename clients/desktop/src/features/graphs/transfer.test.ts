import { describe, expect, it } from 'vitest';
import { canvasDefinition, canvasLayout, graphDrawing, type GraphDefinition } from './files';
import { graphTransfer, parseTransfer, remapTransfer, transferJSON } from './transfer';
import { graphSVG } from './transfer-svg';
import type { Agent } from '../agents/demo';

const worker: Agent = { id: 'worker', name: 'Worker', description: '', enabled: true, defaultHarness: 'codex', model: 'real-model-id', effort: 'high', prompt: 'Implement <task> & verify.' };
const definition: GraphDefinition = {
  name: 'Delivery', description: 'All node types', enabled: true, initial_node: 'plan',
  nodes: {
    plan: { type: 'agent', name: 'Plan', agent: 'worker', choices: ['next'], overrides: { effort: '' } },
    shell: { type: 'terminal', name: 'Check', command: 'echo <ready>', output: { text: '{{ stdout }}' }, to: 'fork' },
    fork: { type: 'fork', name: 'Parallel', branches: { work: { name: 'Work', git_branch: '', separate_worktree: false, output: {}, to: 'join' } } },
    join: { type: 'join', agent: 'worker', prompt: 'Integrate changes', output_branch: 'delivery', choices: ['done'] },
  },
  choices: { next: { name: 'next', input: {}, output: {}, to: 'shell', max_traversals: 3 }, done: { name: 'done', input: {}, output: {}, terminal: true } },
};
const layout = { positions: { plan: { x: -200, y: 30 }, shell: { x: 4000, y: 2000 }, fork: { x: 800, y: 30 }, join: { x: 1200, y: 30 }, 'choice:next': { x: 300, y: 30 }, 'choice:done': { x: 1600, y: 30 } }, viewport: { x: 12, y: 23, zoom: 0.5 } };

describe('graph transfer', () => {
  it('preserves executable settings and layout through JSON and the editor', () => {
    const bundle = graphTransfer(definition, layout, [worker, { ...worker, id: 'unused' }]);
    expect(parseTransfer(transferJSON(bundle))).toEqual(bundle);
    expect(Object.keys(bundle.agents)).toEqual(['worker']);
    const drawing = graphDrawing({ id: 'import', revision: '', ...bundle });
    const result = canvasDefinition(definition, drawing.nodes, drawing.edges);
    expect(result).toMatchObject(definition);
    expect(canvasLayout(drawing.nodes, layout.viewport)).toEqual(layout);
    const existing = { ...worker, id: 'local', name: 'Local', prompt: 'Destination prompt' };
    const remapped = remapTransfer(bundle, { worker: 'local' }, [existing]);
    expect(remapped.definition.nodes.plan.agent).toBe('local');
    expect(remapped.definition.nodes.join.agent).toBe('local');
    expect(remapped.definition.nodes.plan.overrides).toEqual({ effort: '' });
    expect(remapped.agents.local.prompt).toBe('Destination prompt');
    expect(Object.keys(remapped.agents)).toEqual(['local']);
    expect(bundle.definition.nodes.plan.agent).toBe('worker');
  });

  it('rejects malformed and oversized JSON', () => {
    expect(() => parseTransfer('{')).toThrow('Invalid JSON');
    expect(() => parseTransfer(' '.repeat(512 * 1024 + 1))).toThrow('512 KiB');
    expect(() => graphTransfer(definition, layout, [])).toThrow('unavailable');
  });

  it('exports a standalone vector image containing offscreen nodes and escaped labels', () => {
    const drawing = graphDrawing({ id: 'svg', revision: '', definition, layout });
    const svg = graphSVG('<script>alert("x")</script>', drawing.nodes, drawing.edges, { background: '#fff', surface: '#fff', text: '#111', muted: '#666', border: '#ddd' });
    const document = new DOMParser().parseFromString(svg, 'image/svg+xml');
    expect(document.querySelector('parsererror')).toBeNull();
    expect(document.querySelector('script, foreignObject, image')).toBeNull();
    expect(document.querySelector('title')?.textContent).toBe('<script>alert("x")</script>');
    expect(document.documentElement.getAttribute('viewBox')?.split(' ').map(Number)[2]).toBeGreaterThan(4200);
    expect(document.querySelectorAll('path[marker-end]')).toHaveLength(drawing.edges.length);
    expect(document.documentElement.textContent).toContain('echo <ready>');
    expect(document.documentElement.textContent).toContain('Shared workspace');
  });
});
