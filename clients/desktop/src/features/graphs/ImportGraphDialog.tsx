import { useEffect, useId, useRef, useState } from 'react';
import { FileUp, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { Textarea } from '../../design-system/Textarea';
import { request } from '../../engine';
import { IS_MOBILE_HOST } from '../../platform';
import { openMobileGraphFile } from './transfer-files';
import { agentSlug, type Agent } from '../agents/demo';
import { authoringPath, type GraphFile } from './files';
import { GRAPH_TRANSFER_LIMIT, parseTransfer, remapTransfer, type GraphTransfer } from './transfer';

export function ImportGraphDialog({ projectId, agents, graphs, initial, onOpen, onClose }: {
  projectId: string; agents: Agent[]; graphs: GraphFile[]; initial?: GraphTransfer;
  onOpen: (bundle: GraphTransfer) => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const id = useId();
  const [text, setText] = useState('');
  const [bundle, setBundle] = useState(initial);
  const [selections, setSelections] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const duplicate = !!bundle && graphs.some(g => agentSlug(g.definition.name) === agentSlug(bundle.definition.name));
  useEffect(() => { dialog.current?.showModal(); }, []);

  async function loadFile(file: File) {
    if (file.size > GRAPH_TRANSFER_LIMIT) throw new Error('Graph JSON exceeds 512 KiB.');
    setText(await file.text());
  }

  return <dialog ref={dialog} className="settings-dialog graph-import-dialog" aria-labelledby={`${id}-title`} onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <form className="graph-details-form" onSubmit={async event => {
      event.preventDefault();
      if (busy || duplicate) return;
      setError(''); setBusy(true);
      try {
        if (bundle) onOpen(remapTransfer(bundle, selections, agents));
        else setBundle(await request<GraphTransfer>(`${authoringPath(projectId)}/graph-imports/preview`, 'POST', parseTransfer(text)));
      } catch (e) { setError(e instanceof Error ? e.message : 'Could not import graph.'); }
      finally { setBusy(false); }
    }}>
      <div className="dialog-heading"><h2 id={`${id}-title`}>{initial ? 'Import agents' : 'Import graph'}</h2><IconButton label="Close import" disabled={busy} onClick={onClose}><X /></IconButton></div>
      {!bundle ? <>
        <input ref={fileInput} type="file" accept=".json,application/json" hidden onChange={event => {
          const file = event.target.files?.[0]; event.target.value = '';
          if (file) { setError(''); setBusy(true); void loadFile(file).catch(e => setError(e.message)).finally(() => setBusy(false)); }
        }} />
        <Button disabled={busy} onClick={() => {
          if (!IS_MOBILE_HOST) { fileInput.current?.click(); return; }
          setBusy(true); setError('');
          void openMobileGraphFile().then(content => { if (content !== null) setText(content); }).catch(e => setError(e.message)).finally(() => setBusy(false));
        }}><FileUp />Choose JSON file</Button>
        <div className="graph-details-field"><label htmlFor={`${id}-json`}>JSON</label><Textarea id={`${id}-json`} autoFocus spellCheck={false} rows={12} value={text} disabled={busy} onChange={event => setText(event.target.value)} /></div>
      </> : <>
        {!initial && <div className="graph-details-field"><label htmlFor={`${id}-name`}>Graph name</label><Input id={`${id}-name`} autoFocus required maxLength={80} value={bundle.definition.name} aria-invalid={duplicate} onChange={event => setBundle({ ...bundle, definition: { ...bundle.definition, name: event.target.value } })} />{duplicate && <p className="form-error" role="alert">A graph with this name already exists.</p>}</div>}
        <div className="graph-import-agents">{Object.entries(bundle.agents).map(([agentId, agent]) => <div className="graph-details-field" key={agentId}>
          <label htmlFor={`${id}-${agentId}`}>{agent.name}</label>
          <span className="small muted">{agent.defaultHarness} · {agent.model}{agent.effort ? ` · ${agent.effort}` : ''}</span>
          <Select id={`${id}-${agentId}`} label={`Import ${agent.name}`} variant="field" value={Object.hasOwn(selections, agentId) ? selections[agentId] : ''} onChange={event => setSelections(current => ({ ...current, [agentId]: event.target.value }))}>
            <option value="">{agents.some(item => item.id === agentId) ? 'Keep included definition (reuse if identical)' : `Create ${agent.name}`}</option>
            {agents.filter(item => item.enabled).map(item => <option key={item.id} value={item.id}>Use existing: {item.name}</option>)}
          </Select>
        </div>)}</div>
        {!initial && <p className="small muted">{Object.keys(bundle.definition.nodes).length} nodes · {Object.keys(bundle.definition.choices).length} choices · {Object.keys(bundle.agents).length} agents</p>}
      </>}
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="graph-details-actions">
        {bundle && !initial && <Button disabled={busy} onClick={() => { setBundle(undefined); setSelections({}); setError(''); }}>Back</Button>}
        <Button disabled={busy} onClick={onClose}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={busy || duplicate || (bundle ? !agentSlug(bundle.definition.name) : !text.trim())}>{busy ? 'Loading...' : bundle ? initial ? 'Apply' : 'Open editor' : 'Continue'}</Button>
      </div>
    </form>
  </dialog>;
}
