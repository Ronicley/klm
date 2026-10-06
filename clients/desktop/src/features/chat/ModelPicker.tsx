import { ChevronDown, RefreshCw, Check } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import { Slider } from '../../design-system/Slider';
import { HarnessIcon } from './HarnessIcon';
import { type Session, type SessionResponse } from '../../engine';
import { SessionOptions } from './SessionOptions';
import { useModelCatalog } from './modelCatalog';

const effortOrder = ['none', 'off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
function effortRank(value: string) {
  const index = effortOrder.indexOf(value.toLowerCase());
  return index < 0 ? effortOrder.length : index;
}
function effortLabel(value: string) {
  return value === 'xhigh' ? 'Extra high' : value ? value[0].toUpperCase() + value.slice(1) : 'Default';
}

export function ModelPicker({ session, disabled, optionsDisabled = disabled, onSave, onSnapshot }: {
  session: Session; disabled: boolean; optionsDisabled?: boolean; onSave: (model: string, effort: string) => Promise<boolean>;
  onSnapshot?: (snapshot: SessionResponse) => void;
}) {
  const { catalog, loading, error, refresh, revalidate } = useModelCatalog(session.projectId, session.harness, session.id);
  const [menu, setMenu] = useState<'model' | 'effort' | null>(null);
  const [search, setSearch] = useState('');
  const [previewEffort, setPreviewEffort] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [optimistic, setOptimistic] = useState<{ model: string; effort: string } | null>(null);
  const pending = useRef(false);
  useEffect(() => {
    if (optimistic && !saving && (session.model ?? '') === optimistic.model && (session.effort ?? '') === optimistic.effort) setOptimistic(null);
  }, [session.model, session.effort, optimistic, saving]);
  const modelList = useRef<HTMLDivElement>(null);

  const connected = new Set(catalog?.connectedProviders?.map(provider => provider.id) ?? []);
  const models = catalog?.models.filter(item => connected.has(item.provider)) ?? [];
  const chosenModel = optimistic?.model ?? session.model ?? '';
  const chosenEffort = optimistic?.effort ?? session.effort ?? '';
  const currentModelId = chosenModel || catalog?.defaultModel || session.resolvedModel || '';
  const current = models.find(item => item.id === currentModelId);
  let currentEffort = !catalog ? chosenEffort || (currentModelId === session.resolvedModel ? session.resolvedEffort : '') || '' : '';
  if (current?.efforts.length) {
    if (chosenEffort && current.efforts.includes(chosenEffort)) currentEffort = chosenEffort;
    else {
      currentEffort = current.defaultEffort ?? '';
      if (currentModelId === catalog?.defaultModel && catalog.defaultEffort && current.efforts.includes(catalog.defaultEffort)) currentEffort = catalog.defaultEffort;
      else if (!currentEffort && currentModelId === session.resolvedModel && session.resolvedEffort && current.efforts.includes(session.resolvedEffort)) currentEffort = session.resolvedEffort;
    }
  }
  const efforts = [...(current?.efforts ?? [])].sort((a, b) => effortRank(a) - effortRank(b));
  const effortIndex = Math.max(0, efforts.indexOf(previewEffort));
  const filtered = models.filter(option => `${option.name} ${option.id} ${option.providerName}`.toLowerCase().includes(search.toLowerCase()));
  const locked = disabled || saving;

  function openMenu(next: 'model' | 'effort', open: boolean) {
    if (!open) { setMenu(current => current === next ? null : current); return; }
    setSaveError(''); setSearch(''); setPreviewEffort(currentEffort);
    if (!loading) void revalidate();
    setMenu(next);
  }
  async function save(model: string, effort: string, close: boolean) {
    if (pending.current || disabled) return;
    if (model === (session.model ?? '') && effort === (session.effort ?? '')) { if (close) setMenu(null); return; }
    pending.current = true; setSaving(true); setSaveError(''); setOptimistic({ model, effort });
    if (close) setMenu(null);
    try {
      if (!await onSave(model, effort)) { setSaveError('Could not apply selection. Try again.'); setPreviewEffort(currentEffort); setOptimistic(null); }
    } catch { setSaveError('Could not apply selection. Try again.'); setPreviewEffort(currentEffort); setOptimistic(null); }
    finally { pending.current = false; setSaving(false); }
  }
  function chooseModel(id: string) {
    const next = models.find(option => option.id === (id || catalog?.defaultModel));
    const effort = next?.id === currentModelId && next.efforts.includes(session.effort ?? '') ? session.effort! : '';
    void save(id, effort, true);
  }
  function navigateModels(event: KeyboardEvent<HTMLElement>) {
    const options = [...(modelList.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    if (!options.length) return;
    const index = options.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    if (event.key === 'ArrowDown') next = Math.min(index + 1, options.length - 1);
    else if (event.key === 'ArrowUp') next = index <= 0 ? options.length - 1 : index - 1;
    else if (index >= 0 && event.key === 'Home') next = 0;
    else if (index >= 0 && event.key === 'End') next = options.length - 1;
    else return;
    event.preventDefault(); options[next].focus(); options[next].scrollIntoView({ block: 'nearest' });
  }

  return <div className="model-picker-controls">
    {onSnapshot && <SessionOptions session={session} disabled={optionsDisabled || saving} onSnapshot={onSnapshot} />}
    <Menu label="Models" className="model-menu" open={menu === 'model'} onOpenChange={open => openMenu('model', open)} trigger={props => <Button {...props} variant="ghost" size="sm" className="model-picker-trigger" disabled={locked} aria-label="Choose model">
      <HarnessIcon harness={session.harness} /><span className="model-picker-name">{current?.name || currentModelId || (loading ? 'Loading model...' : 'Model unavailable')}</span><ChevronDown />
    </Button>}>
      <div className="model-menu-search"><input data-menu-autofocus type="search" aria-label="Search models" placeholder="Search models" value={search} onChange={event => { setSearch(event.target.value); modelList.current?.scrollTo({ top: 0 }); }} onKeyDown={navigateModels} disabled={saving} /><IconButton label="Refresh models" disabled={loading || saving} onClick={() => void refresh()}><RefreshCw /></IconButton></div>
      {error && <p className="form-error menu-feedback" role="alert">{error}</p>}
      {loading && !catalog ? <p className="menu-feedback muted" role="status">Loading models...</p> : <div ref={modelList} className="model-menu-list" onKeyDown={navigateModels} aria-label="Available models">
        {filtered.map(option => <MenuItem key={option.id} className="model-menu-option" selected={chosenModel === option.id} disabled={locked} title={option.id} onClick={() => chooseModel(option.id)}>
          <span><span className="model-menu-option-name">{option.name || option.id}</span><small>{option.providerName}</small></span>{chosenModel === option.id && <Check />}
        </MenuItem>)}
        {!filtered.length && <p className="menu-feedback muted">{search ? 'No matching models.' : 'No connected models available.'}</p>}
      </div>}
      <div className="model-menu-footer"><MenuItem selected={!chosenModel} disabled={locked || !catalog} onClick={() => chooseModel('')}>Harness default{!chosenModel && <Check />}</MenuItem></div>
      {saveError && <p className="form-error menu-feedback" role="alert">{saveError}</p>}
    </Menu>
    {!!efforts.length && <Menu label="Effort" className="effort-menu" open={menu === 'effort'} onOpenChange={open => openMenu('effort', open)} trigger={props => <Button {...props} variant="ghost" size="sm" className="model-picker-trigger effort-picker-trigger" disabled={locked} aria-label="Choose effort">
      {effortLabel(currentEffort)}<ChevronDown />
    </Button>}>
      <Slider label="Effort" min={0} max={Math.max(0, efforts.length - 1)} value={effortIndex} valueText={effortLabel(efforts[effortIndex] ?? '')} marks={efforts.map((value, index) => ({ value: index, label: effortLabel(value) }))} disabled={locked}
        onValueChange={value => setPreviewEffort(efforts[value])} onValueCommit={value => { const effort = efforts[value]; if (effort !== undefined) void save(session.model ?? '', effort, false); }} />
      {saving && <div className="effort-menu-footer"><span className="small muted" role="status">Saving...</span></div>}
      {saveError && <p className="form-error menu-feedback" role="alert">{saveError}</p>}
    </Menu>}
  </div>;
}
