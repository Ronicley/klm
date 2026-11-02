import { Fragment, useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import type { Project } from '../../engine';
import { getExecutions, getMetrics, type Accounting, type Coverage, type ExecutionPage, type ExecutionUsageCoverage, type Metrics } from './api';
import { ExecutionAnalytics } from './ExecutionAnalytics';
import './metrics.css';

const count = (value: number | null) => value == null ? 'Unavailable' : value.toLocaleString();
const date = (value: string | null) => value ? new Date(value).toLocaleString() : '—';
const label = (value: string | null) => value ? value[0].toUpperCase() + value.slice(1) : '—';
function duration(value: number | null) {
  if (value == null) return 'Unavailable';
  const seconds = Math.floor(value / 1000);
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}
function responseLatency(value: number | null | undefined) {
  if (value == null) return 'Unavailable';
  return value < 1000 ? `${value} ms` : `${(value / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })}s`;
}
function coverageText(coverage?: Coverage) {
  return `${coverage && coverage.known < coverage.total ? 'Partial · ' : ''}${coverage?.known ?? 0} / ${coverage?.total ?? 0} sessions`;
}
function Metric({ name, value, note }: { name: string; value: string; note?: string }) {
  return <div className="metrics-stat"><dt>{name}</dt><dd>{value}</dd>{note && <small>{note}</small>}</div>;
}

function executionCoverageText(c: ExecutionUsageCoverage) {
  return `${c.complete} complete · ${c.partial} partial · ${c.unavailable} unavailable / ${c.total} executions`;
}
function accountingReason(reason?: string) {
  const reasons: Record<string, string> = {
    adapter_not_instrumented: 'Usage unavailable',
    baseline_not_observed: 'Baseline unavailable',
    trustworthy_boundary_baseline_unavailable: 'Verified baseline unavailable',
    final_not_observed: 'Final observation pending',
    final_not_observed_before_exit: 'Final observation unavailable',
    native_identity_mismatch: 'Native identity changed',
    invalid_or_missing_counters: 'Invalid or missing counters',
    counter_reset_or_history_rewrite: 'Counters reset or history changed',
    counter_overflow: 'Counter overflow',
    baseline_timeout: 'Baseline timed out', final_timeout: 'Final observation timed out',
    baseline_request_failed: 'Baseline request failed', final_request_failed: 'Final observation failed',
    baseline_request_write_failed: 'Baseline request could not be sent', final_request_write_failed: 'Final request could not be sent',
    execution_failed: 'Execution failed', execution_cancelled: 'Execution cancelled', execution_interrupted: 'Execution interrupted',
    currency_provenance_not_verified_for_runtime: 'Currency and valuation unverified',
  };
  return reason ? reasons[reason] ?? 'Accounting unavailable' : '';
}
function ExecutionAccounting({ accounting: a }: { accounting: Accounting | null }) {
  if (!a) return <p className="metrics-accounting-unavailable">Accounting unavailable</p>;
  const usage = a.usage;
  const amount = (value: number | null | undefined) => value == null ? 'Unavailable' : `${count(value)}${a.coverage === 'partial' ? ' · partial' : ''}`;
  return <div className="metrics-accounting">
    <div className="metrics-accounting-heading"><strong>Execution usage · {label(a.coverage)}</strong>{a.reason && <span>{accountingReason(a.reason)}</span>}</div>
    <dl className="metrics-accounting-values">
      <Metric name="Input tokens" value={amount(usage?.inputTokens)} />
      <Metric name="Output tokens" value={amount(usage?.outputTokens)} />
      <Metric name="Cache read · included in input" value={amount(usage?.cacheReadTokens)} />
      <Metric name="Cache write · included in input" value={amount(usage?.cacheWriteTokens)} />
      <Metric name="Reasoning tokens" value={amount(usage?.reasoningTokens)} />
      <Metric name="Cost" value="Unavailable" note={accountingReason(a.costReason)} />
    </dl>
    <dl className="metrics-accounting-provenance">
      <div><dt>Source</dt><dd>{a.source === 'pi.get_session_stats' ? 'Pi session statistics · endpoint delta' : 'Unavailable'}</dd></div>
      <div><dt>Protocol / version</dt><dd>{a.protocol} · {a.harnessVersion || 'Version unavailable'}</dd></div>
      <div><dt>Baseline observed</dt><dd>{a.baselineObservedAt ? date(a.baselineObservedAt) : 'Unavailable'}</dd></div>
      <div><dt>Endpoint observed</dt><dd>{a.endpointObservedAt ? date(a.endpointObservedAt) : 'Unavailable'}</dd></div>
      <div><dt>Usage by model</dt><dd>{a.modelAttribution === 'unavailable' ? 'Unavailable' : label(a.modelAttribution)}</dd></div>
    </dl>
  </div>;
}

function ExecutionDetails({ id, refresh }: { id: string; refresh: string }) {
  const [page, setPage] = useState(1);
  const [data, setData] = useState<ExecutionPage | null>(null);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    void getExecutions(id, page, controller.signal).then(setData).catch((error: unknown) => {
      if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load executions.');
    });
    return () => controller.abort();
  }, [id, page, refresh, retry]);
  return <div className="metrics-executions">
    <h3>Observed executions</h3>
    {error ? <p role="alert">{error} <Button size="sm" onClick={() => setRetry(value => value + 1)}>Retry</Button></p> : !data ? <p role="status">Loading executions…</p> : <>
      {!data.total ? <p>No recorded executions.</p> : <>
        <table><thead><tr><th>Started</th><th>Result</th><th>Accumulated time</th><th>Harness / observed models</th></tr></thead><tbody>{data.rows.map(item => <Fragment key={item.id}><tr>
          <td>{date(item.startedAt)}</td><td>{item.outcome ? label(item.outcome) : 'In progress'}{item.interruptionReason && <small>{item.interruptionReason.replaceAll('_', ' ')}</small>}</td>
          <td title={item.partial ? `Observed through ${date(item.observedAt)}` : `Ended ${date(item.endedAt)}`}>{duration(item.durationMs)}{item.partial ? ' · partial' : ''}</td>
          <td>{item.harness}<small>{item.models.join(', ') || 'Model unavailable'}</small><small>{item.role ? label(item.role.replaceAll('_', ' ')) : 'Role unavailable'}</small></td>
        </tr><tr><td colSpan={4}>
          <dl className="metrics-accounting-values">
            <Metric name="First assistant response" value={responseLatency(item.timing?.firstResponseLatencyMs)} note={item.timing?.firstResponseAt ? `Engine emission · ${date(item.timing.firstResponseAt)}` : item.timing && !item.outcome ? 'Not observed yet' : 'Not observed'} />
          </dl>
          <ExecutionAccounting accounting={item.accounting} />
        </td></tr></Fragment>)}</tbody></table>
        <div className="metrics-pagination"><Button size="sm" disabled={page === 1} onClick={() => { setData(null); setPage(value => value - 1); }}>Previous</Button><span>Page {page} / {Math.ceil(data.total / data.pageSize)}</span><Button size="sm" disabled={page * data.pageSize >= data.total} onClick={() => { setData(null); setPage(value => value + 1); }}>Next</Button></div>
      </>}
    </>}
  </div>;
}

export function SessionMetricsDashboard({ projects, visible, onOpen }: { projects: Project[]; visible: boolean; onOpen: (id: string) => void }) {
  const [filters, setFilters] = useState({ projectId: '', harness: '', outcome: '', state: '', sort: 'activity', includeArchived: 'false' });
  const [search, setSearch] = useState('');
  const [querySearch, setQuerySearch] = useState('');
  const [page, setPage] = useState(1);
  const [data, setData] = useState<Metrics | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [details, setDetails] = useState('');
  useEffect(() => { const timer = window.setTimeout(() => { setQuerySearch(search); setPage(1); }, 250); return () => clearTimeout(timer); }, [search]);
  const query = new URLSearchParams({ ...filters, search: querySearch, page: String(page) }).toString();
  useEffect(() => {
    setData(null); setError('');
    if (!visible) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    async function load() {
      if (document.hidden || disposed) return;
      clearTimeout(timer); controller?.abort(); controller = new AbortController();
      const current = controller;
      setLoading(true);
      try {
        const next = await getMetrics(query, current.signal);
        if (!disposed && !current.signal.aborted) { setData(next); setError(''); }
      } catch (error) {
        if (!disposed && !current.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load metrics.');
      } finally {
        if (!disposed && !current.signal.aborted) { setLoading(false); timer = setTimeout(() => void load(), 15000); }
      }
    }
    const visibility = () => {
      if (document.hidden) { clearTimeout(timer); controller?.abort(); } else void load();
    };
    void load(); document.addEventListener('visibilitychange', visibility);
    return () => { disposed = true; clearTimeout(timer); controller?.abort(); document.removeEventListener('visibilitychange', visibility); };
  }, [query, visible, refresh]);
  function filter(key: keyof typeof filters, value: string) { setFilters(current => ({ ...current, [key]: value })); setPage(1); setDetails(''); }
  const summary = data?.summary;
  const coverage = summary?.coverage;
  const observed = summary?.observedExecutionUsage;
  const interruptions = summary?.interruptions;
  const observedAmount = (value: number | null | undefined) => value == null ? 'Unavailable' : `${count(value)}${observed && (observed.coverage.partial > 0 || observed.coverage.unavailable > 0) ? ' · partial' : ''}`;
  return <section className="session-metrics" aria-label="Session metrics">
    <div className="metrics-heading"><div><h2>Session metrics</h2><p>Main conversations · All-time inventory and recorded execution analytics</p></div><Button size="sm" disabled={loading} onClick={() => setRefresh(value => value + 1)}>{loading ? 'Refreshing…' : 'Refresh'}</Button></div>
    <div className="metrics-filters">
      <Input aria-label="Search session titles" placeholder="Search sessions" value={search} onChange={event => setSearch(event.target.value)} />
      <Select label="Project" value={filters.projectId} onChange={event => filter('projectId', event.target.value)}><option value="">All projects</option>{projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}</Select>
      <Select label="Harness" value={filters.harness} onChange={event => filter('harness', event.target.value)}><option value="">All harnesses</option><option value="pi">Pi</option><option value="opencode">OpenCode</option><option value="codex">Codex</option></Select>
      <Select label="Current state" value={filters.state} onChange={event => filter('state', event.target.value)}><option value="">Any current state</option>{['working', 'waiting', 'idle'].map(value => <option key={value} value={value}>{label(value)}</option>)}</Select>
      <Select label="Last execution result" value={filters.outcome} onChange={event => filter('outcome', event.target.value)}><option value="">Any last result</option>{['completed', 'failed', 'cancelled', 'interrupted'].map(value => <option key={value} value={value}>{label(value)}</option>)}</Select>
      <Select label="Sort sessions" value={filters.sort} onChange={event => filter('sort', event.target.value)}><option value="activity">Recent activity</option><option value="tokens">Most tokens</option><option value="duration">Longest accumulated time</option><option value="executions">Most executions</option></Select>
      <label className="metrics-checkbox"><input type="checkbox" checked={filters.includeArchived === 'true'} onChange={event => filter('includeArchived', String(event.target.checked))} />Include archived</label>
    </div>
    <ExecutionAnalytics inventoryQuery={new URLSearchParams({ ...filters, search: querySearch }).toString()} visible={visible} snapshot={data?.updatedAt ?? ''} />
    <h3>All-time inventory · Current activity</h3>
    <p className="metrics-usage-coverage">Period controls apply only to execution analytics above. Inventory filters apply to both sections; current state and last result select conversations, not historical executions.</p>
    {error && <p className="metrics-error" role="alert">{data ? 'Updates unavailable. Showing the last snapshot. ' : ''}{error} <Button size="sm" onClick={() => setRefresh(value => value + 1)}>Retry</Button></p>}
    {!data ? !error && <p role="status">Loading metrics…</p> : <>
      <dl className="metrics-stats" aria-label="All-time native inventory">
        <Metric name="Sessions" value={count(summary!.sessions)} />
        <Metric name="Lifetime tokens" value={count(summary!.tokens)} note={coverageText(coverage?.tokens)} />
        <Metric name="Input tokens" value={count(summary!.inputTokens)} note={coverageText(coverage?.inputTokens)} />
        <Metric name="Output tokens" value={count(summary!.outputTokens)} note={coverageText(coverage?.outputTokens)} />
        <Metric name="Known cost" value="Unavailable" note={coverageText(coverage?.cost)} />
        <Metric name="Average execution cost" value="Unavailable" note="Unavailable" />
      </dl>
      {observed && <div className="metrics-observed-usage">
        <h3>All-time observed execution usage</h3>
        <dl className="metrics-stats metrics-stats--observed" aria-label="Observed execution usage">
          <Metric name="Tokens" value={observedAmount(observed.tokens)} />
          <Metric name="Input tokens" value={observedAmount(observed.inputTokens)} />
          <Metric name="Output tokens" value={observedAmount(observed.outputTokens)} />
          <Metric name="Cost" value="Unavailable" note={executionCoverageText(observed.costCoverage)} />
        </dl>
        <p className="metrics-usage-coverage">{executionCoverageText(observed.coverage)}{observed.reason ? ` · ${accountingReason(observed.reason)}` : ''}</p>
      </div>}
      <dl className="metrics-stats metrics-stats--activity" aria-label="Execution and current activity">
        <Metric name="Accumulated execution time" value={`${duration(summary!.durationMs)}${summary!.durationPartial ? ' · partial' : ''}`} note={coverageText(coverage?.duration)} />
        <Metric name="Observed executions" value={count(summary!.executions)} note={coverageText(coverage?.executions)} />
        <Metric name="Failed executions" value={count(summary!.failed)} note={`${count(summary!.cancelled)} cancelled · ${count(summary!.interrupted)} interrupted`} />
        <Metric name="Recorded interruptions" value={count(interruptions?.total ?? null)} note={interruptions ? `${count(interruptions.stop)} Stop · ${count(interruptions.replacement)} replacement · ${count(interruptions.restart)} restart · ${count(interruptions.shutdown)} shutdown · ${count(interruptions.other)} other` : 'Unavailable'} />
        <Metric name="Working now" value={count(summary!.working)} />
        <Metric name="Waiting for you" value={count(summary!.waiting)} />
      </dl>
      <div className="metrics-snapshot"><span>Updated {date(data.updatedAt)}</span><span>Execution coverage begins with recorded turns; earlier turns are unknown.</span></div>
      {!data.total ? <div className="metrics-empty">No sessions match these filters.</div> : <>
        <div className="metrics-table-wrap"><table className="metrics-table"><thead><tr><th>Session / project</th><th>Harness / observed models</th><th>Current / last result</th><th>Lifetime tokens</th><th>Cost</th><th>Accumulated time</th><th>Executions</th><th>Last activity</th></tr></thead><tbody>
          {data.rows.map(row => <Fragment key={row.id}><tr>
            <td><button className="metrics-session-link" onClick={() => onOpen(row.id)}>{row.title}</button><small>{row.project}{row.archived ? ' · Archived' : ''}</small><button className="metrics-details-button" aria-expanded={details === row.id} onClick={() => setDetails(current => current === row.id ? '' : row.id)}>{details === row.id ? 'Hide executions' : 'Executions'}</button></td>
            <td>{row.harness}<small>{row.models.join(', ') || '—'}</small></td>
            <td><span className={`metrics-state metrics-state--${row.currentState}`}>{label(row.currentState)}</span><small>{label(row.lastOutcome)}</small></td>
            <td title={`Input: ${count(row.inputTokens)} · Output: ${count(row.outputTokens)}`}>{count(row.tokens)}</td><td>Unavailable</td>
            <td title={row.observedSince ? `Observed since ${date(row.observedSince)}` : 'Unavailable'}>{duration(row.durationMs)}{row.durationPartial && <small>Partial</small>}</td><td>{count(row.executions)}</td><td>{date(row.lastActivityAt)}</td>
          </tr>{details === row.id && <tr><td colSpan={8}><ExecutionDetails id={row.id} refresh={data.updatedAt} /></td></tr>}</Fragment>)}
        </tbody></table></div>
        <div className="metrics-pagination"><span>{data.total.toLocaleString()} sessions</span><Button size="sm" disabled={page === 1} onClick={() => setPage(value => value - 1)}>Previous</Button><span>Page {page} / {Math.max(1, Math.ceil(data.total / data.pageSize))}</span><Button size="sm" disabled={page * data.pageSize >= data.total} onClick={() => setPage(value => value + 1)}>Next</Button></div>
      </>}
    </>}
  </section>;
}
