import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Activity, ArrowDownRight, ArrowUpRight, ChevronLeft, ChevronRight, Clock3, GitBranch, Layers3, MessagesSquare } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Select } from '../../design-system/Select';
import { TabNav } from '../../design-system/TabNav';
import { useAnalytics } from './useAnalytics';
import { compact, coverageTitle, dateLabel, hours, metricText, weekdays } from './format';
import type { Metric, SeriesBucket, Totals } from './analyticsApi';
import './analytics.css';
import { DEFAULT_ENGINE_TIME_ZONE, getEngineSettings } from '../automations/api';

type Tab = 'overview' | 'topics' | 'graphs';
type ChartMetric = 'workingMs' | 'tokens';
function Panel({ title, detail, children, className = '' }: { title: string; detail?: ReactNode; children: ReactNode; className?: string }) {
  return <section className={`analytics-card ${className}`}><header><h3>{title}</h3>{detail && <span>{detail}</span>}</header>{children}</section>;
}
function Value({ metric, format = compact }: { metric: Metric; format?: (n: number) => string }) {
  const text = metricText(metric, format);
  const detail = coverageTitle(metric);
  return <span className="analytics-value" title={detail || undefined} aria-label={detail ? `${text}. ${detail}` : undefined}>{text}{metric.provisional && <small className="analytics-coverage">Live</small>}</span>;
}
function Delta({ current, previous }: { current: Metric; previous?: Metric }) {
  if (current.coverage !== 'complete' || previous?.coverage !== 'complete' || current.provisional || previous.provisional || current.value == null || previous.value == null || previous.value === 0) return null;
  const delta = (current.value - previous.value) / previous.value * 100;
  return <span className="analytics-delta">{delta >= 0 ? <ArrowUpRight /> : <ArrowDownRight />}{Math.abs(delta).toFixed(1)}% <span>vs. previous</span></span>;
}
function TopicScrollArea({ children, className, label, visible }: { children: ReactNode; className: string; label: string; visible: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const element = root.current;
    if (!visible || !element) return;
    const rows = [...element.querySelectorAll<HTMLElement>('[data-analytics-topic-row]')];
    const footer = element.querySelector('tfoot');
    const measure = () => {
      if (!element.clientWidth) return;
      // Measure actual rows so wrapped names, font sizing and narrow layouts
      // still show five topics rather than relying on a fixed pixel row height.
      const height = rows.length > 5 ? Math.ceil(rows[4].getBoundingClientRect().bottom - element.getBoundingClientRect().top + element.scrollTop + parseFloat(getComputedStyle(element).paddingBottom) + (footer?.getBoundingClientRect().height ?? 0)) : null;
      element.style.maxHeight = height === null ? '' : `${height}px`;
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    rows.slice(0, 5).forEach(row => observer.observe(row));
    if (footer) observer.observe(footer);
    return () => observer.disconnect();
  }, [children, visible]);
  return <div ref={root} className={`${className} analytics-topic-scroll`} tabIndex={0} role="region" aria-label={label}>{children}</div>;
}
function Ranking({ rows, topics = false, visible = true }: { rows: { key: string; name: string; detail?: string; metric: Metric; color?: string }[]; topics?: boolean; visible?: boolean }) {
  const total = rows.reduce((sum, row) => sum + (row.metric.value ?? 0), 0);
  const percentages = rows.every(row => row.metric.coverage === 'complete' && row.metric.value !== null);
  const content = <>{rows.map(row => <div key={row.key} data-analytics-topic-row={topics || undefined}>
    <div><span title={row.detail}>{row.name}</span><strong><Value metric={row.metric} format={hours} />{percentages && total > 0 && <small>{Math.round((row.metric.value ?? 0) / total * 100)}%</small>}</strong></div>
    <div className="analytics-track"><span style={{ width: `${total ? (row.metric.value ?? 0) / total * 100 : 0}%`, background: row.color }} /></div>
  </div>)}{!rows.length && <p className="analytics-footnote">No recorded activity.</p>}</>;
  return topics ? <TopicScrollArea className="analytics-ranking" label="Work by topic" visible={visible}>{content}</TopicScrollArea> : <div className="analytics-ranking">{content}</div>;
}
function WorkChart({ groups, metric, allTime }: { groups: SeriesBucket[]; metric: ChartMetric; allTime: boolean }) {
  const gradient = useId();
  const [hovered, setHovered] = useState<number | null>(null);
  const values = groups.map(group => group.future ? null : group[metric].value);
  const previous = groups.map(group => {
    const before = metric === 'workingMs' ? group.previousWorkingMs : group.previousTokens;
    return group.future || group[metric].coverage !== 'complete' || before?.coverage !== 'complete' || before.provisional || group[metric].provisional ? null : before.value;
  });
  const hasPrevious = previous.some(value => value !== null);
  const max = Math.max(...values.map(value => value ?? 0), ...previous.map(value => value ?? 0), 1) * 1.18;
  const x = (i: number) => groups.length <= 1 ? 370 : 52 + i * 636 / (groups.length - 1);
  const y = (v: number) => 208 - v / max * 172;
  const format = metric === 'workingMs' ? hours : compact;
  const segments = (rows: (number | null)[]) => {
    const result: string[][] = []; let points: string[] = [];
    rows.forEach((value, index) => { if (value === null) { if (points.length) result.push(points); points = []; } else points.push(`${x(index)},${y(value)}`); });
    if (points.length) result.push(points);
    return result;
  };
  const lastIndex = values.reduce<number>((last, value, index) => value !== null ? index : last, -1);
  const selected = Math.max(0, Math.min(groups.length - 1, hovered ?? (lastIndex < 0 ? 0 : lastIndex)));
  const group = groups[selected];
  return <div className="analytics-chart">
    <div className="analytics-chart-caption"><strong>{group?.label ?? 'No observations'}</strong><span>{group && (group.future ? 'Upcoming' : <Value metric={group[metric]} format={format} />)}{group && previous[selected] !== null && <small> / {format(previous[selected]!)} previous</small>}</span></div>
    <svg viewBox="0 0 720 252" role="img" aria-label={`${metric === 'workingMs' ? 'Agent-hours' : 'Tokens'} by ${allTime ? 'week range' : 'weekday'}`}>
      <defs><linearGradient id={gradient} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="var(--color-accent)" stopOpacity=".17" /><stop offset="100%" stopColor="var(--color-accent)" stopOpacity="0" /></linearGradient></defs>
      {[0, 1, 2, 3].map(i => <g key={i}><line x1="52" x2="688" y1={y(max * i / 3)} y2={y(max * i / 3)} className="analytics-grid-line" /><text x="0" y={y(max * i / 3) + 4}>{format(max * i / 3)}</text></g>)}
      {segments(values).map((points, i) => <g key={i}><polygon points={`${points[0].split(',')[0]},208 ${points.join(' ')} ${points[points.length - 1].split(',')[0]},208`} fill={`url(#${gradient})`} /><polyline points={points.join(' ')} className="analytics-line" /></g>)}
      {segments(previous).map((points, i) => <polyline key={i} points={points.join(' ')} className="analytics-line analytics-line--previous" />)}
      {groups.map((item, i) => {
        const label = `${item.label}: ${item.future ? 'Upcoming' : [metricText(item[metric], format), coverageTitle(item[metric])].filter(Boolean).join('. ')} (${item.start} – ${item.endExclusive})`;
        return <g key={item.key}><text x={x(i)} y="240" textAnchor="middle">{item.label}</text>{values[i] !== null && <circle cx={x(i)} cy={y(values[i]!)} r={selected === i ? 5 : 3} className="analytics-point" />}<rect x={x(i) - 22} y="15" width="44" height="204" fill="transparent" tabIndex={0} role="button" aria-label={label} onMouseEnter={() => setHovered(i)} onFocus={() => setHovered(i)} onClick={() => setHovered(i)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setHovered(i); } }}><title>{label}</title></rect></g>;
      })}
    </svg>
    <div className="analytics-legend"><span><i />Selected period</span>{hasPrevious && <span><i className="is-previous" />Previous period</span>}</div>
  </div>;
}
const topicColumns: { key: keyof Pick<Totals, 'sessionsUsed' | 'sessionsCreated' | 'workingMs' | 'inputTokens' | 'outputTokens' | 'totalTokens'>; name: string; format?: (n: number) => string }[] = [
  { key: 'sessionsUsed', name: 'Sessions used' }, { key: 'sessionsCreated', name: 'Created' }, { key: 'workingMs', name: 'Agent-hours', format: hours }, { key: 'inputTokens', name: 'Input tokens' }, { key: 'outputTokens', name: 'Output tokens' }, { key: 'totalTokens', name: 'Total tokens' },
];

export function AnalyticsPage({ visible, projects }: { visible: boolean; projects: { id: string; name: string }[] }) {
  const [tab, setTab] = useState<Tab>('overview');
  const [period, setPeriod] = useState('current');
  const [compare, setCompare] = useState(true);
  const [project, setProject] = useState('all');
  const [metric, setMetric] = useState<ChartMetric>('workingMs');
  const [timeZone, setTimeZone] = useState(DEFAULT_ENGINE_TIME_ZONE);
  useEffect(() => { const load = () => { void getEngineSettings().then(value => setTimeZone(value.timeZone)).catch(() => {}); }; load(); window.addEventListener('engine-time-zone-changed', load); return () => window.removeEventListener('engine-time-zone-changed', load); }, []);
  useEffect(() => { if (project !== 'all' && !projects.some(item => item.id === project)) setProject('all'); }, [project, projects]);
  const feed = useAnalytics(visible, { period, compare, project, timeZone });
  const data = feed.data;
  const allTime = period === 'all';
  const weeks = data?.availableWeeks ?? [];
  const index = weeks.findIndex(week => week.startDate === (period === 'current' ? weeks.find(item => item.current)?.startDate : period));
  const selectWeek = (date: string) => setPeriod(weeks.find(week => week.startDate === date)?.current ? 'current' : date);
  const total = data?.summary;
  const before = data?.comparison?.summary;
  const topicRows = [...(data?.topics ?? [])].sort((left, right) => {
    const a = left.totals.workingMs.value, b = right.totals.workingMs.value;
    if (a === null && b !== null) return 1;
    if (b === null && a !== null) return -1;
    return (b ?? 0) - (a ?? 0) || left.key.localeCompare(right.key);
  });
  const maxDaily = Math.max(...(data?.weekdays.map(day => day.workingMs.value ?? 0) ?? []), 1);
  const scopeDetail = data ? `${data.timeZone} · ${data.period.start} – ${data.period.endExclusive}. Collection started ${data.collectionStartedAt}.` : timeZone;
  return <div className="analytics-page" role="region" aria-label="Analytics">
    <TabNav<Tab> value={tab} onChange={setTab} items={[{ value: 'overview', label: 'Overview' }, { value: 'topics', label: 'Topics' }, { value: 'graphs', label: 'Graphs' }]} />
    <div className="analytics-toolbar" title={scopeDetail}>
      <div className="analytics-period"><IconButton label="Previous week" disabled={allTime || index <= 0} onClick={() => selectWeek(weeks[index - 1].startDate)}><ChevronLeft /></IconButton><Select label={`Analytics period (${timeZone})`} value={period} onChange={event => setPeriod(event.target.value)}><option value="all">All time</option>{!weeks.length && <option value={period === 'all' ? 'current' : period}>{period === 'current' || period === 'all' ? 'This week' : dateLabel(period)}</option>}{[...weeks].reverse().map(item => <option key={item.startDate} value={item.current ? 'current' : item.startDate}>{item.current ? 'This week' : `${dateLabel(item.startDate)} – ${dateLabel(item.endDate)}`}</option>)}</Select><IconButton label="Next week" disabled={allTime || index < 0 || index >= weeks.length - 1} onClick={() => selectWeek(weeks[index + 1].startDate)}><ChevronRight /></IconButton></div>
      <span className="analytics-vs">vs.</span><Select label="Compare analytics" value={compare && !allTime ? 'previous' : 'none'} disabled={allTime} onChange={event => setCompare(event.target.value === 'previous')}><option value="previous">{data?.period.current ? 'Previous week to date' : 'Previous week'}</option><option value="none">No comparison</option></Select>
      <Select label="Filter analytics by project" className="analytics-project-filter" value={project} onChange={event => setProject(event.target.value)}><option value="all">All projects</option>{projects.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</Select>
    </div>
    <div className="analytics-content" aria-busy={feed.loading}>
      {feed.error && <div className="analytics-feedback" role="alert">{data ? 'Analytics could not refresh. Showing the last snapshot. ' : ''}{feed.error}<Button size="sm" variant="ghost" onClick={feed.refresh}>Retry</Button></div>}
      {!data && <Panel title={feed.error ? 'Analytics unavailable' : 'Loading Analytics'}><p className="analytics-footnote" role="status">{feed.error ? 'Retry to load this scope.' : 'Loading observations…'}</p></Panel>}
      {data && total && <>
        {tab === 'overview' && <section className="analytics-kpis" aria-label="Activity totals">
          {[
            { name: 'Sessions used', value: total.sessionsUsed, detail: <><Value metric={total.sessionsCreated} /> created</>, icon: <MessagesSquare />, previous: before?.sessionsUsed },
            { name: 'Agents executed', value: total.agentsExecuted, detail: <><Value metric={total.nativeSubagents} /> native subagents</>, icon: <Layers3 />, previous: before?.agentsExecuted },
            { name: 'Agent-hours', value: total.workingMs, format: hours, detail: 'Execution time', icon: <Clock3 />, previous: before?.workingMs },
            { name: 'Total tokens', value: total.totalTokens, detail: <><Value metric={total.inputTokens} /> in · <Value metric={total.outputTokens} /> out</>, icon: <Activity />, previous: before?.totalTokens },
            { name: 'Graph runs', value: total.graphRuns, detail: <><Value metric={total.graphsUsed} /> graphs · <Value metric={total.graphWallMs} format={hours} /> wall-clock</>, icon: <GitBranch />, previous: before?.graphRuns },
          ].map(kpi => <div className="analytics-kpi" key={kpi.name}><div>{kpi.icon}<span>{kpi.name}</span></div><strong><Value metric={kpi.value} format={kpi.format} /></strong><small>{kpi.detail}</small>{compare && !allTime && <Delta current={kpi.value} previous={kpi.previous} />}</div>)}
        </section>}
        {tab === 'overview' && <>
          <div className="analytics-main-grid">
            <Panel title="Activity over time" detail={<Select label="Chart metric" value={metric} onChange={event => setMetric(event.target.value as ChartMetric)}><option value="workingMs">Agent-hours</option><option value="tokens">Tokens</option></Select>}><WorkChart key={`${period}:${project}:${metric}`} groups={data.series} metric={metric} allTime={allTime} /></Panel>
            <Panel title="Work by topic" detail="Agent-hours"><Ranking topics visible={visible} rows={topicRows.map(topic => ({ key: topic.key, name: topic.name, detail: topic.projectName, metric: topic.totals.workingMs, color: `var(--topic-${topic.color})` }))} /></Panel>
          </div>
          <div className="analytics-lower-grid">
            <Panel title={allTime ? 'Work by weekday' : 'Daily rhythm'} detail="Agent-hours"><div className="analytics-weekdays">{data.weekdays.map(day => <div key={day.weekday}><span>{day.future ? '—' : <Value metric={day.workingMs} format={hours} />}</span><div>{!day.future && day.workingMs.value !== null && day.workingMs.value > 0 && <i style={{ height: `${day.workingMs.value / maxDaily * 100}%` }} title={`${weekdays[day.weekday]}: ${metricText(day.workingMs, hours)}`} />}</div><strong>{weekdays[day.weekday]}</strong></div>)}</div></Panel>
            <Panel title="Harnesses" detail="Agent-hours"><Ranking rows={data.harnesses.map(item => ({ key: item.id, name: item.name, metric: item.workingMs }))} /></Panel>
            <Panel title="Time breakdown"><div className="analytics-time"><strong><Value metric={total.workingMs} format={hours} /><small>Agent execution</small></strong><div className="analytics-time-bar"><i style={{ width: `${(total.workingMs.value ?? 0) / Math.max(1, (total.workingMs.value ?? 0) + (total.blockedMs.value ?? 0)) * 100}%` }} /></div><p><span>Identified blocked waits</span><b><Value metric={total.blockedMs} format={hours} /></b></p><p><span>Graph wall-clock</span><b><Value metric={total.graphWallMs} format={hours} /></b></p><small>Graph time is separate from agent-hours.</small></div></Panel>
          </div>
          <Panel title="Activity calendar" detail={<span title={data.timeZone}>Last 13 weeks · Agent-hours</span>} className="analytics-calendar-card"><div className="analytics-calendar"><div className="analytics-calendar-days">{weekdays.map(day => <span key={day}>{day}</span>)}</div><div className="analytics-calendar-weeks">{weeks.map((item, weekIndex) => <div key={item.startDate} className={!allTime && data.period.weekStart === item.startDate ? 'is-selected' : ''}><span>{weekIndex % 3 === 0 ? dateLabel(item.startDate) : ''}</span>{data.calendar.days.filter(day => day.weekStart === item.startDate).map(day => {
            const value = day.workingMs.value;
            const label = `${dateLabel(day.date)} · ${day.future ? 'Upcoming' : [metricText(day.workingMs, hours), coverageTitle(day.workingMs)].filter(Boolean).join('. ')} · ${data.timeZone}`;
            return <button key={day.date} type="button" disabled={day.future} className={day.future ? 'is-future' : value === null ? 'is-unavailable' : undefined} aria-label={`${label}. View week.`} title={label} onClick={() => selectWeek(day.weekStart)} style={{ '--heat': day.future || value === null || value === 0 ? 'var(--color-subtle)' : `color-mix(in srgb, var(--color-accent) ${Math.min(95, 18 + value / 960000)}%, var(--color-surface))` } as CSSProperties} />;
          })}</div>)}</div></div><div className="analytics-calendar-footer"><span><Value metric={data.calendar.activeDays} /> active days</span><span>Less {[0, 1, 2, 3, 4].map(level => <i key={level} style={{ background: `color-mix(in srgb, var(--color-accent) ${level * 23}%, var(--color-subtle))` }} />)} More</span></div></Panel>
        </>}
        {tab === 'topics' && <Panel title="Topics" detail="Current session membership"><TopicScrollArea className="analytics-table-scroll analytics-topic-table" label="Topics" visible={visible}><table className="analytics-table"><thead><tr><th>Topic</th>{topicColumns.map(column => <th key={column.key}>{column.name}</th>)}</tr></thead><tbody>{topicRows.map(topic => <tr key={topic.key} data-analytics-topic-row><td><span className="analytics-topic-dot" style={{ background: `var(--topic-${topic.color})` }} /><strong>{topic.name}</strong><small>{topic.projectName}</small></td>{topicColumns.map(column => <td key={column.key}><Value metric={topic.totals[column.key]} format={column.format} /></td>)}</tr>)}</tbody><tfoot><tr><th>Total</th>{topicColumns.map(column => <td key={column.key}><Value metric={total[column.key]} format={column.format} /></td>)}</tr></tfoot></table></TopicScrollArea></Panel>}
        {tab === 'graphs' && <><Panel title="Graphs" detail={<><Value metric={total.graphsUsed} /> used</>}><div className="analytics-table-scroll"><table className="analytics-table"><thead><tr><th>Graph</th><th>Runs</th><th>Run hours</th><th title="Average observed in-period wall-clock per contributing timed run">Average run</th></tr></thead><tbody>{data.graphs.map(graph => <tr key={graph.key}><td><strong>{graph.name}</strong><small>{graph.projectName}</small></td><td><Value metric={graph.runs} /></td><td><Value metric={graph.wallMs} format={hours} /></td><td title={`${graph.timedRuns} contributing timed runs; in-period average`}><Value metric={graph.averageMs} format={hours} /></td></tr>)}</tbody><tfoot><tr><th>Total</th><td><Value metric={total.graphRuns} /></td><td><Value metric={total.graphWallMs} format={hours} /></td><td title={`${total.graphTimedRuns} contributing timed runs; in-period average`}><Value metric={total.graphAverageMs} format={hours} /></td></tr></tfoot></table></div></Panel><p className="analytics-footnote">Run hours measure wall-clock per run, not the sum of its agents.</p></>}
      </>}
    </div>
  </div>;
}
