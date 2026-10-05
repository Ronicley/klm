import { useRef, useState } from 'react';
import { EllipsisVertical, ShieldQuestion, TriangleAlert } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import { request, type PermissionDecision, type PermissionRequest, type SessionResponse } from '../../engine';

export function PermissionCard({ permission, sessionId, projectName, origin, onResolved }: {
  permission: PermissionRequest; sessionId: string; projectName: string; origin?: string; onResolved: (session: SessionResponse) => void;
}) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);
  const pending = useRef(false);
  const busy = submitting || permission.resolving;
  async function decide(decision: PermissionDecision) {
    if (pending.current || permission.resolving) return;
    setMenuOpen(false);
    pending.current = true; setSubmitting(true); setError('');
    try {
      onResolved(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(sessionId)}/permissions/${encodeURIComponent(permission.id)}`, 'POST', { decision }));
    } catch (error) { setError(error instanceof Error ? error.message : 'Permission decision could not be sent. Please retry.'); }
    finally { pending.current = false; setSubmitting(false); }
  }
  const harness = permission.harness === 'opencode' ? 'OpenCode' : permission.harness === 'pi' ? 'Pi' : 'Codex';
  return <section className="permission-card" aria-label={`${harness} permission request`}>
    <div className="permission-heading"><ShieldQuestion aria-hidden="true" /><strong>{permission.title || 'Permission required'}</strong>{permission.dangerous && <span className="permission-danger" role="img" aria-label="Potentially destructive operation" title="May permanently delete files or discard changes"><TriangleAlert aria-hidden="true" /></span>}<span>{harness}</span></div>
    {origin && <p className="permission-description">{origin}</p>}
    {permission.command ? <div className="permission-command">
      <div><span>Command</span><pre><code>{permission.command}</code></pre></div>
      {permission.path ? <div><span>Working directory</span><code>{permission.path}</code></div> : null}
    </div> : <>
      {permission.description && permission.description !== permission.patterns.join('\n') ? <p className="permission-description">{permission.description}</p> : null}
      {permission.patterns.length > 0 ? <ul className="permission-patterns">{permission.patterns.map((pattern, index) => <li key={index}><code>{pattern}</code></li>)}</ul> : null}
      {permission.details ? <details className="permission-details"><summary>Request details</summary><pre>{JSON.stringify(permission.details, null, 2)}</pre></details> : null}
    </>}
    {permission.scopeLabel && <p className="permission-scope">{permission.scopeLabel}</p>}
    <div className="permission-actions">
      {permission.decisions.includes('once') && <Button size="sm" variant="primary" disabled={busy} onClick={() => void decide('once')}>{permission.allowLabel || 'Allow'}</Button>}
      {permission.decisions.includes('session') && <Button size="sm" disabled={busy} onClick={() => void decide('session')}>Allow session</Button>}
      {permission.decisions.includes('reject') && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void decide('reject')}>Deny</Button>}
      {(permission.decisions.includes('always') || permission.decisions.includes('allow_global') || permission.decisions.includes('deny_global')) && <Menu label="Permission options" role="menu" open={menuOpen} onOpenChange={setMenuOpen} trigger={props => <IconButton {...props} label="Permission options" size="sm" disabled={busy}><EllipsisVertical /></IconButton>}>
        <div className="permission-menu-scope">{permission.scopeLabel || 'This exact request'}</div>
        {permission.decisions.includes('always') && <MenuItem role="menuitem" disabled={busy} title={projectName} onClick={() => void decide('always')}>Allow in this project</MenuItem>}
        {permission.decisions.includes('deny_project') && <MenuItem role="menuitem" disabled={busy} title={projectName} onClick={() => void decide('deny_project')}>Deny in this project</MenuItem>}
        {permission.decisions.includes('allow_global') && <MenuItem role="menuitem" disabled={busy} onClick={() => void decide('allow_global')}>Allow globally</MenuItem>}
        {permission.decisions.includes('deny_global') && <MenuItem role="menuitem" disabled={busy} onClick={() => void decide('deny_global')}>Deny globally</MenuItem>}
      </Menu>}
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
  </section>;
}
