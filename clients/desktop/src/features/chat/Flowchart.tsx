import { Expand, Minus, Plus, X } from 'lucide-react';
import { memo, useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Button, IconButton } from '../../design-system/Button';

type Diagram = { source: string; theme: string; url: string; width: number; height: number };
let renderer: Promise<typeof import('mermaid')> | undefined;
let renderQueue: Promise<unknown> = Promise.resolve();
let nextDiagramId = 0;

function renderDiagram(source: string, theme: string): Promise<Diagram> {
  const task = renderQueue.then(async () => {
    // Keep agent-provided configuration, HTML and remote resources out of the
    // rendering document. Flowcharts only; unsupported input stays readable.
    if (!/^\s*(?:flowchart|graph)\s+(?:TB|TD|BT|RL|LR)\b/.test(source)
      || source.length > 50_000 || /%%\s*\{|\bimg\s*:|url\s*\(|@import|<\s*(?:img|image|iframe|object|svg)\b/i.test(source)) {
      throw new Error('Unsupported flowchart source');
    }
    const { default: mermaid } = await (renderer ??= import('mermaid'));
    const styles = getComputedStyle(document.documentElement);
    const color = (token: string) => styles.getPropertyValue(token).trim();
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      suppressErrorRendering: true,
      maxTextSize: 50_000,
      maxEdges: 500,
      theme: 'base',
      fontFamily: styles.getPropertyValue('--font-sans').trim(),
      htmlLabels: false,
      flowchart: { htmlLabels: false, useMaxWidth: false },
      themeVariables: {
        darkMode: theme.startsWith('dark:'),
        primaryColor: color('--color-accent-soft'),
        primaryTextColor: color('--color-text'),
        primaryBorderColor: color('--color-accent'),
        lineColor: color('--color-muted'),
        secondaryColor: color('--color-subtle'),
        tertiaryColor: color('--color-sidebar'),
        edgeLabelBackground: color('--color-surface'),
        clusterBkg: color('--color-sidebar'),
        clusterBorder: color('--color-muted'),
      },
    });
    const container = document.createElement('div');
    container.className = 'flowchart-render-target';
    container.setAttribute('aria-hidden', 'true');
    document.body.append(container);
    try {
      const { svg } = await mermaid.render(`klm-flowchart-${++nextDiagramId}`, source, container);
      const documentSvg = new DOMParser().parseFromString(svg, 'image/svg+xml');
      const root = documentSvg.documentElement;
      const viewBox = root.getAttribute('viewBox')?.trim().split(/[\s,]+/).map(Number);
      const width = viewBox?.[2];
      const height = viewBox?.[3];
      if (!width || !height || !Number.isFinite(width) || !Number.isFinite(height) || width < 0 || height < 0) throw new Error('Invalid diagram dimensions');
      root.setAttribute('width', String(width));
      root.setAttribute('height', String(height));
      // An image keeps generated SVG isolated from the chat DOM. SVG images
      // cannot execute scripts, navigate links or load external subresources.
      const url = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(new XMLSerializer().serializeToString(root))}`;
      return { source, theme, url, width, height };
    } finally {
      container.remove();
    }
  });
  renderQueue = task.catch(() => undefined);
  return task;
}

function ExpandedFlowchart({ diagram, onClose }: { diagram: Diagram; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const [zoom, setZoom] = useState(1);
  function fit() {
    const area = viewport.current;
    if (area) setZoom(Math.min(1, (area.clientWidth - 32) / diagram.width, (area.clientHeight - 32) / diagram.height));
  }
  useEffect(() => {
    dialog.current?.showModal();
    fit();
  }, [diagram.width, diagram.height]);
  return createPortal(<dialog ref={dialog} className="settings-dialog flowchart-dialog" aria-labelledby={titleId} onCancel={onClose} onClose={onClose}>
    <div className="dialog-heading flowchart-toolbar">
      <h2 id={titleId}>Flowchart</h2>
      <div className="flowchart-controls">
        <IconButton label="Zoom out" disabled={zoom <= 0.05} onClick={() => setZoom(value => Math.max(0.05, value / 1.25))}><Minus /></IconButton>
        <span className="flowchart-zoom">{Math.round(zoom * 100)}%</span>
        <IconButton label="Zoom in" disabled={zoom >= 4} onClick={() => setZoom(value => Math.min(4, value * 1.25))}><Plus /></IconButton>
        <Button size="sm" variant="ghost" onClick={fit}>Fit</Button>
        <IconButton label="Close flowchart" onClick={onClose}><X /></IconButton>
      </div>
    </div>
    <div ref={viewport} className="flowchart-viewport" role="region" aria-label="Expanded flowchart" tabIndex={0}>
      <div className="flowchart-canvas"><img src={diagram.url} alt="Flowchart" draggable={false} style={{ width: diagram.width * zoom, height: diagram.height * zoom }} /></div>
    </div>
  </dialog>, document.body);
}

function currentDiagramTheme() {
  const { theme = 'light', themeVariant = '' } = document.documentElement.dataset;
  return `${theme}:${themeVariant}`;
}

export const Flowchart = memo(function Flowchart({ source }: { source: string }) {
  const [theme, setTheme] = useState(currentDiagramTheme);
  const [diagram, setDiagram] = useState<Diagram | null>(null);
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(currentDiagramTheme()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'data-theme-variant'] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    let cancelled = false;
    // Avoid parsing every token while the harness streams a diagram.
    const timer = window.setTimeout(() => {
      void renderDiagram(source, theme).then(result => {
        if (!cancelled) { setDiagram(result); setFailedSource(null); }
      }).catch(() => { if (!cancelled) setFailedSource(source); });
    }, 250);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [source, theme]);
  const current = diagram?.source === source && diagram.theme === theme ? diagram : null;
  return <div className="flowchart-block">
    {current ? <>
      <div className="flowchart-inline-toolbar"><Button size="sm" variant="ghost" onClick={() => setExpanded(true)}><Expand />Expand</Button></div>
      <img className="flowchart-preview" src={current.url} alt="Flowchart" width={current.width} height={current.height} />
      {expanded && <ExpandedFlowchart diagram={current} onClose={() => setExpanded(false)} />}
    </> : <>
      {failedSource === source && <p className="flowchart-error" role="status">Could not render flowchart. Source shown below.</p>}
      <pre tabIndex={0} aria-label="Flowchart source"><code>{source}</code></pre>
    </>}
  </div>;
});
