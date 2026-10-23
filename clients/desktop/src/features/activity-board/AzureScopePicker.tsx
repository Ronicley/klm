import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { SearchSelect } from '../../design-system/SearchSelect';
import { discoverAzureOptions, type BoardValue, type ConnectionDraft } from './api';

export function AzureScopePicker({ draft, busy, run, onChange, onSelectType }: {
  draft: ConnectionDraft; busy: boolean;
  run: (action: () => Promise<void>) => Promise<void>;
  onChange: (draft: ConnectionDraft) => void;
  onSelectType: (draft: ConnectionDraft) => void;
}) {
  const [projects, setProjects] = useState<BoardValue[]>();
  const [types, setTypes] = useState<BoardValue[]>();
  const fixed = !!draft.connectionId;
  useEffect(() => { setProjects(undefined); setTypes(undefined); }, [draft.scope.organization, draft.token, draft.connectionId]);
  const options = (values: BoardValue[]) => values.map(value => ({ value: value.id, label: value.name }));
  const projectOptions = fixed ? [{ id: draft.scope.projectId ?? '', name: draft.scope.project ?? '' }] : projects ?? [];
  const typeOptions = fixed ? [{ id: draft.scope.workItemType ?? '', name: draft.scope.workItemType ?? '' }] : types ?? [];
  const emptyMappings = () => Object.fromEntries(Object.keys(draft.mappings).map(id => [id, []]));

  function loadTypes(next: ConnectionDraft) {
    setTypes(undefined);
    void run(async () => {
      const result = await discoverAzureOptions(next, 'work-item-types');
      setTypes(result.values);
      onChange({ ...next, scope: result.scope });
    });
  }

  return <>
    {!fixed && <Button disabled={busy || !draft.scope.organization?.trim() || !draft.token} onClick={() => {
      const next = { ...draft, scope: { organization: draft.scope.organization }, mappings: emptyMappings() };
      onChange(next); setProjects(undefined); setTypes(undefined);
      void run(async () => { setProjects((await discoverAzureOptions(next, 'projects')).values); });
    }}>Load projects</Button>}
    <div className="board-picker-field"><span>Azure project</span><SearchSelect label="Azure project" placeholder="Select a project" disabled={busy || fixed || !projects?.length} value={draft.scope.projectId ?? ''} options={options(projectOptions)} emptyMessage="No accessible projects" onChange={id => {
      const selected = projects?.find(project => project.id === id);
      if (!selected) return;
      const next = { ...draft, scope: { organization: draft.scope.organization, project: selected.name, projectId: selected.id }, mappings: emptyMappings() };
      onChange(next); loadTypes(next);
    }} /></div>
    {projects?.length === 0 && <p className="muted" role="status">No accessible projects found for this organization and token.</p>}
    <div className="board-picker-field"><span>Work item type</span><SearchSelect label="Work item type" placeholder="Select a work item type" disabled={busy || fixed || !types?.length} value={draft.scope.workItemType ?? ''} options={options(typeOptions)} emptyMessage="No work item types" onChange={id => {
      const selected = types?.find(kind => kind.id === id);
      if (selected) onSelectType({ ...draft, scope: { ...draft.scope, workItemType: selected.name }, mappings: emptyMappings() });
    }} /></div>
    {types?.length === 0 && <p className="muted" role="status">No work item types found in this project.</p>}
    {!fixed && draft.scope.projectId && !types && <Button disabled={busy} onClick={() => loadTypes(draft)}>Load work item types</Button>}
  </>;
}
