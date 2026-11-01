import { useContext, useId } from 'react';
import { ChevronDown, GitFork, Plus, Trash2, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Toggle } from '../../design-system/Toggle';
import { ChoiceOutputEditor } from './ChoiceOutputEditor';
import { ChoiceOutputPreview } from './ChoiceOutputPreview';
import { normalizeChoiceName, outputVariables } from './choice';
import { createForkBranch, forkOutputError, gitBranchNameError, type ForkBranch, type ForkDefinition } from './fork';
import { forgetDrafts, useEngineDraft } from '../drafts/draftStore';
import { GraphDraftScope } from './draftScope';

export function ForkNodePanel({ fork, destinations, payloadFields = [], onSave, onClose }: {
  fork: ForkDefinition;
  destinations: Record<string, string>;
  payloadFields?: string[];
  onSave: (fork: ForkDefinition) => void;
  onClose: () => void;
}) {
  const id = useId();
  const key = useContext(GraphDraftScope);
  const [draft, setDraft, clearDraft] = useEngineDraft(`${key}.fork`, fork);
  const [expandedId, setExpandedId] = useEngineDraft<string | null>(`${key}.expanded`, fork.branches[0]?.id ?? null);
  const names = draft.branches.map(branch => branch.name.trim());
  const duplicate = names.some((name, index) => name && names.indexOf(name) !== index);
  const gitBranches = draft.branches.filter(branch => branch.separateWorktree !== false).map(branch => branch.gitBranch).filter(Boolean);
  const duplicateGitBranches = new Set(gitBranches).size !== gitBranches.length;
  const valid = !!draft.name.trim() && draft.branches.length >= 2 && !duplicate && !duplicateGitBranches && draft.branches.every(branch => branch.name.trim() && !forkOutputError(branch.outputFields) && (branch.separateWorktree === false || !gitBranchNameError(branch.gitBranch)));

  function updateBranch(branchId: string, update: Partial<ForkBranch>) {
    setDraft(current => ({ ...current, branches: current.branches.map(branch => branch.id === branchId ? { ...branch, ...update } : branch) }));
  }

  return <aside className="graph-agent-panel graph-fork-panel nodrag nopan nowheel" role="dialog" aria-labelledby={`${id}-title`} onKeyDown={event => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.stopPropagation(); onClose(); }
  }}>
    <form className="graph-choice-form" onSubmit={event => {
      event.preventDefault();
      if (valid) { onSave({ name: draft.name.trim(), branches: draft.branches.map(branch => ({ ...branch, name: branch.name.trim(), outputFields: branch.outputFields.map(field => ({ ...field, name: normalizeChoiceName(field.name, true) })) })) }); clearDraft(draft); }
    }}>
      <div className="graph-agent-panel-header"><h2 id={`${id}-title`}>Fork</h2><IconButton label="Close fork settings" onClick={onClose}><X /></IconButton></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} autoFocus required maxLength={80} value={draft.name} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} /></div>
      <div className="graph-choice-payload">
        <div className="graph-agent-overrides-header"><h3>Branches</h3><Button size="sm" variant="ghost" onClick={() => {
          const branch = createForkBranch();
          setDraft(current => ({ ...current, branches: [...current.branches, branch] }));
          setExpandedId(branch.id);
        }}><Plus />Add branch</Button></div>
        <div className="graph-fork-branch-list">{draft.branches.map((branch, index) => {
          const expanded = expandedId === branch.id;
          const fieldId = `${id}-${branch.id}`;
          const outputError = forkOutputError(branch.outputFields);
          const nameError = branch.name.trim() ? '' : 'Enter a name.';
          const isolated = branch.separateWorktree !== false;
          const gitNameError = isolated ? gitBranchNameError(branch.gitBranch) : '';
          return <GraphDraftScope.Provider key={branch.id} value={`${key}.branch:${branch.id}`}><section className="graph-fork-branch-section">
            <div className="graph-fork-branch-heading">
              <Button variant="ghost" className="graph-fork-branch-tab" aria-expanded={expanded} aria-controls={`${fieldId}-form`} onClick={() => setExpandedId(expanded ? null : branch.id)}>
                <GitFork /><span>{branch.name || `Branch ${index + 1}`}</span>{(nameError || outputError || gitNameError) && <span className="graph-fork-incomplete" aria-label="Incomplete branch">!</span>}<ChevronDown className="graph-fork-chevron" />
              </Button>
              <IconButton label={`Remove branch ${branch.name || index + 1}`} disabled={draft.branches.length <= 2} onClick={() => {
                setDraft(current => ({ ...current, branches: current.branches.filter(item => item.id !== branch.id) }));
                if (expanded) setExpandedId(null);
              }}><Trash2 /></IconButton>
            </div>
            {expanded && <div id={`${fieldId}-form`} className="graph-fork-branch-editor">
              <div className="graph-agent-panel-field"><label htmlFor={`${fieldId}-name`}>Name</label><Input id={`${fieldId}-name`} autoFocus={!branch.name} required maxLength={80} value={branch.name} aria-invalid={!!nameError} aria-describedby={nameError ? `${fieldId}-name-error` : undefined} onChange={event => updateBranch(branch.id, { name: event.target.value })} />{nameError && <p id={`${fieldId}-name-error`} className="form-error" role="alert">{nameError}</p>}</div>
              <div className="graph-fork-destination"><span>Destination</span><strong>{destinations[branch.id] || 'Not connected'}</strong></div>
              <div className="graph-fork-workspace">
                <Toggle label="Separate worktree" checked={isolated} onCheckedChange={separateWorktree => updateBranch(branch.id, { separateWorktree })} />
                {isolated ? <>
                  <div className="graph-agent-panel-field"><label htmlFor={`${fieldId}-git-branch`}>Git branch name</label><Input id={`${fieldId}-git-branch`} required placeholder="parallel/write-tests" autoCapitalize="none" autoCorrect="off" spellCheck={false} value={branch.gitBranch} aria-invalid={!!gitNameError} aria-describedby={gitNameError ? `${fieldId}-git-name-error` : undefined} onChange={event => updateBranch(branch.id, { gitBranch: event.target.value })} />{gitNameError && <p id={`${fieldId}-git-name-error`} className="form-error" role="alert">{gitNameError}</p>}</div>
                  <div className="graph-fork-destination"><span>Base revision</span><strong>Incoming revision</strong></div>
                </> : <div className="graph-fork-destination"><span>Workspace</span><strong>Inherited directory and Git branch</strong></div>}
              </div>
              <ChoiceOutputEditor fields={branch.outputFields} variables={outputVariables('fork', payloadFields)} payloadContext onChange={outputFields => updateBranch(branch.id, { outputFields })} />
              {outputError && <p className="form-error" role="alert">{outputError}</p>}
              {!outputError && branch.outputFields.length > 0 && <ChoiceOutputPreview context="fork" inputFields={[]} outputFields={branch.outputFields} />}
            </div>}
          </section></GraphDraftScope.Provider>;
        })}</div>
        {duplicate && <p className="form-error" role="alert">Each branch needs a unique name.</p>}
        {duplicateGitBranches && <p className="form-error" role="alert">Use a different Git branch for each worktree.</p>}
      </div>
      <div className="graph-choice-form-actions"><Button onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={!valid}>Apply</Button></div>
    </form>
  </aside>;
}
