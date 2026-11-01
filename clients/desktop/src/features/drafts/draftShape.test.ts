import { expect, it } from 'vitest';
import { matchesDraftContainer } from './draftShape';

it('accepts and restores roots -> directory without requiring initial on the directory variant', () => {
  const initial = { kind: 'roots', initial: 'C:\\Projects' };
  const restored = JSON.parse(JSON.stringify({ kind: 'directory', path: 'C:\\Projects\\App' }));
  expect(matchesDraftContainer(restored, initial)).toBe(true);
  expect(matchesDraftContainer({ kind: 'roots' }, initial)).toBe(true);
});

it('preserves numeric/boolean defaults edited as raw strings or cleared optional fields', () => {
  const initial = { count: 12, enabled: true };
  const restored = JSON.parse(JSON.stringify({ count: '1e', enabled: undefined }));
  expect(matchesDraftContainer(restored, initial)).toBe(true);
  expect(restored.count).toBe('1e');
  expect(restored).not.toHaveProperty('enabled');
});

it('does not infer schemas from transport variants or changing graph/entity maps', () => {
  expect(matchesDraftContainer({ transport: 'stdio', command: 'server', args: [] }, { transport: 'http', url: 'https://mcp.example' })).toBe(true);
  expect(matchesDraftContainer({ nodes: { new: {} }, revision: '2' }, { nodes: { old: {} }, revision: 0 })).toBe(true);
  expect(matchesDraftContainer('new', null)).toBe(true);
  expect(matchesDraftContainer({ id: 'entity' }, undefined)).toBe(true);
  expect(matchesDraftContainer(undefined, 'initially-expanded-branch')).toBe(true);
  expect(matchesDraftContainer(null, { id: 'initially-selected-entity' })).toBe(true);
  const collapsed = JSON.parse(JSON.stringify({ expanded: null }));
  expect(matchesDraftContainer(collapsed.expanded, 'initially-expanded-branch')).toBe(true);
  expect(collapsed).toHaveProperty('expanded', null);
});

it('still rejects incompatible outer containers without inspecting raw child values', () => {
  expect(matchesDraftContainer([], {})).toBe(false);
  expect(matchesDraftContainer('raw', {})).toBe(false);
  expect(matchesDraftContainer({}, [])).toBe(false);
  expect(matchesDraftContainer(12, '')).toBe(false);
});
