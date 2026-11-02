import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { getAnalytics, type Analytics, type AnalyticsMeasure, type ExecutionUsageCoverage } from './api';

const coverage = (c: ExecutionUsageCoverage) => `${c.complete} complete · ${c.partial} partial · ${c.unavailable} unavailable / ${c.total}`;
const partial = (c: ExecutionUsageCoverage) => c.partial > 0 || c.unavailable > 0 ? ' · partial' : '';
function Cells({ value: m }: { value: AnalyticsMeasure }) {
  return <><td>{m.executions.toLocaleString()}</td>
    <td>{m.durationMs == null ? 'Unavailable' : `${(m.durationMs / 3600000).toLocaleString(undefined, { maximumFractionDigits: 2 })}h${partial(m.durationCoverage)}`}<small>{coverage(m.durationCoverage)}</small></td>
    <td>{m.usage.tokens == null ? 'Unavailable' : `${m.usage.tokens.toLocaleString()}${partial(m.usage.coverage)}`}<small>{coverage(m.usage.coverage)}</small>{m.usage.reason && <small>Counter overflow</small>}</td></>;
}
function Table({ heading, rows }: { heading: string; rows: { key: string; label: string; detail?: string; value: AnalyticsMeasure }[] }) {
  return <div className="metrics-analytics-table"><h4>{heading}</h4><div className="metrics-table-wrap"><table><thead><tr><th>{heading}</th><th>Execution starts</th><th>Observed time in period</th><th>Execution-end tokens · unallocated model</th></tr></thead>
    <tbody>{rows.map(row => <tr key={row.key}><td>{row.label}{row.detail && <small>{row.detail}</small>}</td><Cells value={row.value} /></tr>)}</tbody></table></div></div>;
}

export function ExecutionAnalytics({ inventoryQuery, visible, snapshot }: { inventoryQuery: string; visible: boolean; snapshot: string }) {
  const [draft, setDraft] = useState(() => ({ from: new Date(Date.now() - 30 * 86400000).toISOString(), to: new Date().toISOString(), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', bucket: 'daily' }));
  const [period, setPeriod] = useState(draft);
  const [data, setData] = useState<Analytics | null>(null);
  const [error, setError] = useState('');
  const [validation, setValidation] = useState('');
  const [retry, setRetry] = useState(0);
  const query = `${inventoryQuery}&${new URLSearchParams(period)}`;
  // Inventory polling already pauses while hidden. Reuse its snapshot wake-up.
  useEffect(() => { setData(null); setError(''); }, [query]);
  useEffect(() => {
    if (!visible || document.hidden) return;
    const controller = new AbortController();
    void getAnalytics(query, controller.signal).then(next => { if (!controller.signal.aborted) { setData(next); setError(''); } }).catch((e: unknown) => {
      if (!controller.signal.aborted) setError(e instanceof Error ? e.message : 'Could not load analytics.');
    });
    return () => controller.abort();
  }, [query, visible, snapshot, retry]);
  function apply() {
    const explicit = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;
    const from = new Date(draft.from), to = new Date(draft.to);
    if (!explicit.test(draft.from) || !explicit.test(draft.to) || !Number.isFinite(from.getTime()) || !Number.isFinite(to.getTime()) || to <= from) {
      setValidation('Enter explicit RFC3339 instants with offsets, with to after from.'); return;
    }
    setValidation('');
    setPeriod({ ...draft, from: from.toISOString(), to: to.toISOString() });
    setRetry(value => value + 1);
  }
  const a = data?.analytics;
  return <section className="metrics-analytics" aria-label="Period execution analytics">
    <h3>Period execution analytics</h3>
    <div className="metrics-filters metrics-period-filters">
      <label>From · inclusive<Input aria-label="Period from inclusive" value={draft.from} onChange={e => setDraft(v => ({ ...v, from: e.target.value }))} /></label>
      <label>To · exclusive<Input aria-label="Period to exclusive" value={draft.to} onChange={e => setDraft(v => ({ ...v, to: e.target.value }))} /></label>
      <label>IANA timezone<Input aria-label="Bucket IANA timezone" value={draft.timezone} onChange={e => setDraft(v => ({ ...v, timezone: e.target.value }))} /></label>
      <Select label="Calendar buckets" value={draft.bucket} onChange={e => setDraft(v => ({ ...v, bucket: e.target.value }))}><option value="daily">Daily</option><option value="weekly">Weekly · Monday</option></Select>
      <Button size="sm" onClick={apply}>Apply period</Button>
    </div>
    {validation && <p className="metrics-error" role="alert">{validation}</p>}
    {error && <p className="metrics-error" role="alert">{data ? 'Updates unavailable. Showing the last snapshot. ' : ''}{error} <Button size="sm" onClick={() => setRetry(v => v + 1)}>Retry</Button></p>}
    {!a ? !error && <p role="status">Loading analytics…</p> : <>
      <p className="metrics-usage-coverage">[{a.period.from}, {a.period.to}) · {a.period.timezone} · Updated {new Date(data!.updatedAt).toLocaleString()}</p>
      <p className="metrics-usage-coverage">Execution starts use the start date. Time is clipped to the period and additive across parallel executions. Tokens use the execution end as an approximation, not the consumption date.</p>
      <p className="metrics-usage-coverage">{a.sessionsWithHistory} / {a.matchedSessions} matching sessions have recorded history. {a.undatedUsageExecutions} undated usage observations excluded across matching history. Model usage is unallocated. Cost unavailable.</p>
      <Table heading="Period total" rows={[{ key: 'total', label: 'Recorded executions', value: a.summary }]} />
      <Table heading="Project" rows={a.projects.map(g => ({ key: g.id, label: g.label, value: g }))} />
      <Table heading="Execution harness" rows={a.harnesses.map(g => ({ key: g.id, label: g.label, value: g }))} />
      <Table heading={`${a.period.bucket === 'weekly' ? 'Week starting' : 'Day'} · ${a.period.timezone}`} rows={a.buckets.map(b => ({ key: b.from, label: b.label, detail: `[${b.from}, ${b.to})`, value: b }))} />
    </>}
  </section>;
}
