import { useEffect, useRef, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { ENGINE_URL, EngineRequestError, findHarnessInstallation, getHarnessInventory, installHarness, type Harness, type HarnessInstallPlan, type HarnessInstallRequest, type HarnessInventory } from '../../engine';
import { engineStorageKey } from '../../engineIdentity';
import './harness-settings.css';

type PendingInstall = { id: Harness['id']; request: HarnessInstallRequest };
const pendingKey = () => engineStorageKey(`klm.harness-install.v1:${ENGINE_URL}`);
function readPending(): PendingInstall | undefined {
  try { const value = JSON.parse(localStorage.getItem(pendingKey()) ?? 'null') as PendingInstall | null; return value?.request.operationId ? value : undefined; } catch { return undefined; }
}
function savePending(value?: PendingInstall) { try { if (value) localStorage.setItem(pendingKey(), JSON.stringify(value)); else localStorage.removeItem(pendingKey()); } catch { /* The visible request retains its operation ID even when storage is unavailable. */ } }
function failure(error: unknown) { return error instanceof Error ? error.message : 'Could not load harness installation status.'; }

function InstallConfirmation({ plan, host, name, onDecision }: { plan: HarnessInstallPlan; host: string; name: string; onDecision: (confirmed: boolean) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog harness-install-confirmation" aria-labelledby="harness-install-title"
    onClose={event => event.stopPropagation()}
    onCancel={event => { event.preventDefault(); event.stopPropagation(); onDecision(false); }}>
    <h2 id="harness-install-title">Install {name} {plan.version}?</h2>
    <p>This changes the global installation on <strong>{host}</strong> for ordinary terminal use, including other engines using the same packages.</p>
    <dl><dt>Package</dt><dd>{plan.package}@{plan.version}</dd><dt>Global prefix</dt><dd>{plan.destination}</dd>
      {plan.replace && <><dt>Replace version</dt><dd>{plan.currentVersion || 'Unknown / broken'} → {plan.version}</dd></>}
      <dt>Prerequisites</dt><dd>{plan.prerequisites.bootstrap ? plan.prerequisites.version ? `Keep Node.js ${plan.prerequisites.version} and install missing npm 10.9.4 from the verified official Node.js distribution.` : <>Install official Node.js 22.22.0 with npm at <code>{plan.prerequisites.node}</code>. On Windows, add its directory to the engine user’s PATH.</> : `Keep Node.js ${plan.prerequisites.version} and npm ${plan.prerequisites.npmVersion}.`}</dd>
      <dt>Permissions</dt><dd>Uses the engine user’s existing OS permissions.</dd>
    </dl>
    <div className="dialog-actions"><Button autoFocus onClick={() => onDecision(false)}>Cancel</Button><Button variant="primary" onClick={() => onDecision(true)}>Install globally</Button></div>
  </dialog>;
}

export function HarnessSettings() {
  const [inventory, setInventory] = useState<HarnessInventory>();
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [loading, setLoading] = useState(true);
  const [posting, setPosting] = useState(false);
  const [pending, setPending] = useState(readPending);
  const [confirming, setConfirming] = useState<HarnessInstallPlan>();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const reconciling = useRef(false);
  const mounted = useRef(true);

  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
    let reading = false;
    async function load(cached: boolean) {
      if (reading || controller.signal.aborted || document.hidden) return;
      reading = true;
      try { const next = await getHarnessInventory(cached, controller.signal); if (!controller.signal.aborted) { setInventory(next); setLoadError(''); } }
      catch (error) { if (!controller.signal.aborted) setLoadError(failure(error)); }
      finally { reading = false; if (!controller.signal.aborted) { setLoading(false); timer = setTimeout(() => void load(true), 3000); } }
    }
    const visible = () => { if (!document.hidden) { clearTimeout(timer); void load(true); } };
    void load(false); document.addEventListener('visibilitychange', visible);
    return () => { controller.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', visible); };
  }, [refreshVersion]);

  // A persisted operation is reconciled by its exact ID. Never repeat its POST
  // automatically after a timeout, disconnect or closing/reopening Settings.
  useEffect(() => {
    if (!pending || !inventory?.jobs.some(job => job.operationId === pending.request.operationId)) return;
    savePending(); setPending(undefined);
  }, [pending, inventory]);

  async function submit(value: PendingInstall) {
    if (posting) return;
    setPosting(true); setError(''); setPending(value); savePending(value);
    try {
      await installHarness(value.id, value.request);
      savePending();
      if (mounted.current) { setPending(undefined); setRefreshVersion(n => n + 1); }
    } catch (error) {
      // Only a definite nonacceptance can discard the operation ID. A server
      // error or transport loss remains uncertain and requires explicit lookup.
      if (error instanceof EngineRequestError && [400, 403, 404, 409, 426].includes(error.status)) { savePending(); if (mounted.current) setPending(undefined); }
      if (mounted.current) setError(failure(error));
    } finally { if (mounted.current) setPosting(false); }
  }

  async function reconcile() {
    if (!pending || reconciling.current) return;
    reconciling.current = true; setLoading(true);
    try { await findHarnessInstallation(pending.request.operationId); savePending(); setPending(undefined); setRefreshVersion(n => n + 1); }
    catch (error) { setError(`${failure(error)} Check again before retrying this same request.`); }
    finally { reconciling.current = false; setLoading(false); }
  }

  function decide(confirmed: boolean) {
    const plan = confirming; setConfirming(undefined);
    if (!confirmed || !plan) return;
    void submit({ id: plan.harnessId, request: { operationId: crypto.randomUUID(), planToken: plan.token, confirmGlobal: true, confirmReplacement: plan.replace, confirmPrerequisites: plan.prerequisites.bootstrap } });
  }

  const busy = posting || !!inventory?.activeJobId || !!pending;
  return <section className="harness-settings" aria-labelledby="harness-settings-title">
    <div className="settings-panel-title"><h3 id="harness-settings-title">Harnesses</h3><Button size="sm" disabled={loading || posting} onClick={() => { setLoading(true); setRefreshVersion(n => n + 1); }}><RefreshCw aria-hidden="true" />Refresh</Button></div>
    {inventory && <p className="muted harness-host">{inventory.host} · {inventory.platform}</p>}
    {loading && !inventory && <p role="status">Checking harnesses…</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {loadError && <p className="form-error" role="alert">{loadError}</p>}
    {pending && <div className="harness-uncertain" role="status"><p>Installation delivery is unconfirmed.</p><code>{pending.request.operationId}</code><div className="dialog-actions"><Button size="sm" disabled={posting || loading} onClick={() => void reconcile()}>Check receipt</Button><Button size="sm" disabled={posting || loading} onClick={() => void submit(pending)}>Retry same request</Button></div></div>}
    <div className="harness-rows">{inventory?.harnesses.map(harness => {
      const plan = inventory.plans.find(p => p.harnessId === harness.id);
      const job = inventory.jobs.filter(j => j.harnessId === harness.id).at(-1);
      const active = !!job && job.id === inventory.activeJobId;
      const installed = harness.installedVersion || plan?.currentVersion;
      const status = active ? job.step : harness.available && harness.compatible ? 'Installed' : installed ? 'Different or unverified version' : 'Not installed';
      return <article className="harness-row" key={harness.id} aria-labelledby={`harness-name-${harness.id}`}>
        <div className="harness-row-heading"><div><h4 id={`harness-name-${harness.id}`}>{harness.name}</h4><span className="harness-version">{installed || 'Not installed'}</span></div>
          <Button size="sm" disabled={busy || loading || !plan || !!plan.blocked || plan.noop} onClick={() => setConfirming(plan)}>{active ? job.step : plan?.noop ? 'Installed' : installed ? `Install ${harness.requiredVersion}` : 'Install'}</Button></div>
        <p className={harness.compatible ? 'settings-saved' : 'muted'} role="status">{status}{!harness.compatible && harness.requiredVersion ? ` · Required ${harness.requiredVersion}` : ''}</p>
        {plan?.blocked && <p className="form-error">{plan.blocked}</p>}
        {harness.executionReason && <p className="muted">{harness.executionReason}</p>}
        {job?.error && !active && <p className="form-error" role="status">{job.step}: {job.error}</p>}
        <details><summary>Details</summary><dl><dt>Effective executable</dt><dd>{harness.path || 'Not found'}</dd><dt>Source</dt><dd>{harness.source || 'Not found'}</dd><dt>Global prefix</dt><dd>{plan?.destination || 'Checking…'}</dd>
          {plan && <><dt>Node.js / npm</dt><dd>{plan.prerequisites.bootstrap ? 'Not installed' : `${plan.prerequisites.version || 'Unknown'} / ${plan.prerequisites.npmVersion || 'Unknown'}`}</dd></>}
        </dl>{job && <pre className="harness-install-log" aria-label={`${harness.name} installation output`}>{job.log.join('\n')}</pre>}</details>
      </article>;
    })}</div>
    {inventory?.activeJobId && <p role="status" className="muted">Global installation in progress. You can close Settings and return later.</p>}
    {confirming && inventory && <InstallConfirmation plan={confirming} host={inventory.host} name={inventory.harnesses.find(h => h.id === confirming.harnessId)?.name ?? confirming.harnessId} onDecision={decide} />}
  </section>;
}
