import { getSmoothStepPath, Position, type Edge } from '@xyflow/react';
import type { CanvasNode } from './GraphCanvas';

type Palette = { background: string; surface: string; text: string; muted: string; border: string };
const escapeXML = (text: string) => text.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '').replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&apos;' })[c]!);
const text = (x: number, y: number, value: string, fill: string, size = 13, bold = false) => `<text x="${x}" y="${y}" fill="${escapeXML(fill)}" font-size="${size}"${bold ? ' font-weight="600"' : ''}>${escapeXML(value)}</text>`;

function wrap(value: string, width: number) {
  const limit = Math.max(12, Math.floor((width - 32) / 7.5));
  return value.replace(/\s+/g, ' ').match(new RegExp(`.{1,${limit}}(?:\\s|$)|.{1,${limit}}`, 'gu'))?.map(line => line.trim()) ?? [''];
}

export function graphSVG(title: string, nodes: CanvasNode[], edges: Edge[], palette: Palette): string {
  const cards = nodes.filter(n => n.type !== 'initialPlaceholder').map(node => {
    const d = node.data;
    const width = node.measured?.width ?? (node.type === 'choice' ? 220 : 280);
    const kind = node.type === 'aiAgent' ? 'AI agent' : node.type === 'terminal' ? 'Terminal command' : node.type === 'choice' ? 'Choice' : node.type === 'fork' ? 'Fork' : 'Join';
    const name = d.choice?.name || d.fork?.name || d.terminal?.name || d.name || d.agent?.name || kind;
    const details = d.terminal ? [d.terminal.command] : d.agent ? [d.agent.name, d.settings?.model?.name || d.agent.model || '', d.settings?.harness || d.agent.defaultHarness || ''] : [];
    const lines = [name, ...details].flatMap(value => wrap(value, width));
    let bottom = 48 + lines.length * 20;
    const branches = (d.fork?.branches ?? []).map(branch => {
      const label = wrap(branch.name, width);
      const detail = wrap(branch.separateWorktree === false ? 'Shared workspace' : branch.gitBranch || 'Worktree', width);
      const y = bottom;
      bottom += (label.length + detail.length) * 18 + 8;
      return { id: branch.id, label, detail, y };
    });
    const height = Math.max(node.measured?.height ?? 0, bottom + 16);
    return { node, width, height, kind, lines, branches };
  });
  if (!cards.length) throw new Error('Add a node before exporting SVG.');
  const byID = new Map(cards.map(card => [card.node.id, card]));
  const minX = Math.min(...cards.map(c => c.node.position.x)) - 80;
  const minY = Math.min(...cards.map(c => c.node.position.y)) - 80;
  const width = Math.max(...cards.map(c => c.node.position.x + c.width)) - minX + 80;
  const height = Math.max(...cards.map(c => c.node.position.y + c.height)) - minY + 80;
  const paths = edges.flatMap(edge => {
    const source = byID.get(edge.source), target = byID.get(edge.target);
    if (!source || !target) return [];
    const branch = source.branches.find(branch => branch.id === edge.sourceHandle);
    const sourceY = source.node.position.y + (branch ? branch.y + 8 : source.height / 2);
    const [path] = getSmoothStepPath({ sourceX: source.node.position.x + source.width, sourceY, targetX: target.node.position.x, targetY: target.node.position.y + target.height / 2, sourcePosition: Position.Right, targetPosition: Position.Left, borderRadius: 12 });
    return [`<path d="${escapeXML(path)}" fill="none" stroke="${escapeXML(palette.muted)}" stroke-width="2" marker-end="url(#arrow)"/>`];
  }).join('');
  const shapes = cards.map(({ node, width: w, height: h, kind, lines, branches }) => `<g transform="translate(${node.position.x} ${node.position.y})"><rect width="${w}" height="${h}" rx="12" fill="${escapeXML(palette.surface)}" stroke="${escapeXML(palette.border)}"/>${text(16, 25, `${kind}${node.data.initial ? ' · Start' : ''}`, palette.muted, 12)}${lines.map((line, index) => text(16, 53 + index * 20, line, index ? palette.muted : palette.text, index ? 13 : 14, !index)).join('')}${branches.map(branch => `${branch.label.map((line, i) => text(16, branch.y + i * 18 + 6, line, palette.text)).join('')}${branch.detail.map((line, i) => text(16, branch.y + (branch.label.length + i) * 18 + 6, line, palette.muted, 11)).join('')}`).join('')}</g>`).join('');
  return `<?xml version="1.0" encoding="UTF-8"?>\n<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="${minX} ${minY} ${width} ${height}" role="img" aria-labelledby="title"><title id="title">${escapeXML(title)}</title><defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="${escapeXML(palette.muted)}"/></marker></defs><rect x="${minX}" y="${minY}" width="${width}" height="${height}" fill="${escapeXML(palette.background)}"/><g font-family="Inter,Arial,sans-serif">${paths}${shapes}</g></svg>`;
}

export function graphPalette(element: Element): Palette {
  const style = getComputedStyle(element);
  const color = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
  return { background: color('--color-surface', '#ffffff'), surface: color('--color-surface', '#ffffff'), text: color('--color-text', '#17212b'), muted: color('--color-muted', '#64748b'), border: color('--color-border', '#d9e0e7') };
}
