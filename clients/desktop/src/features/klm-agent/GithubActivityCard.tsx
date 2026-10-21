import { useCallback, useEffect, useState } from 'react';
import { Bookmark, CircleCheck, CircleDot, CircleX, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, MessageSquare, Radio, ShieldAlert } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { EngineRequestError, type Project } from '../../engine';
import { getActivityDetails, safeActivityUrl, type GithubActivityItem, type GithubActivityDetails } from './githubActivityApi';
import { usePipelineMembers } from './usePipelineMembers';

const icons: Record<string, typeof GitPullRequest> = { pr: GitPullRequest, merge: GitMerge, issue: CircleDot, comment: MessageSquare, commit: GitCommitHorizontal, security: ShieldAlert, pipeline: Radio, event: CircleDot };
function receivedTime(stamp: string, now: number) {
  const seconds = Math.max(0, Math.floor((now - Date.parse(stamp)) / 1000));
  if (seconds < 60) return 'Just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

function ActivityDetails({ eventId, onUnavailable }: { eventId: string; onUnavailable: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const [details, setDetails] = useState<GithubActivityDetails>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!expanded || details) return;
    const controller = new AbortController(); setError('');
    void getActivityDetails(eventId, controller.signal).then(value => { if (!controller.signal.aborted) setDetails(value); }).catch(cause => {
      if (controller.signal.aborted) return;
      if (cause instanceof EngineRequestError && cause.status === 404) onUnavailable();
      else setError(cause instanceof Error ? cause.message : 'Details could not be loaded.');
    });
    return () => controller.abort();
  }, [expanded, details, eventId, attempt, onUnavailable]);
  return <><Button variant="ghost" size="sm" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><MessageSquare />{expanded ? 'Hide details' : 'Details'}</Button>
    {expanded && <div className="activity-detail">
      {error ? <p role="alert">{error} <Button size="sm" variant="ghost" onClick={() => setAttempt(value => value + 1)}>Retry</Button></p> : !details ? <p role="status">Loading details…</p> : <>
        <dl>{details.fields.map((field, index) => <div key={index}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}</dl>
        {details.body && <p>{details.body}</p>}
        {details.entries && <ul>{details.entries.map((entry, index) => <li key={index}>{safeActivityUrl(entry.url) ? <a href={safeActivityUrl(entry.url)} target="_blank" rel="noopener noreferrer">{entry.text}</a> : entry.text}</li>)}</ul>}
        {details.truncated && <p>Details truncated.</p>}
      </>}
    </div>}
  </>;
}

function PipelineChild({ item }: { item: GithubActivityItem }) {
  const m = item.pipeline;
  const status = m?.conclusion || m?.status;
  const active = !m?.conclusion && ['in_progress', 'queued', 'waiting', 'pending', 'requested'].includes(m?.status ?? '');
  const url = safeActivityUrl(item.targetUrl);
  return <li className={`activity-pipeline-child activity-tone--${item.tone}`} data-activity-member-id={item.id}>
    <span className={`activity-pipeline-dot${active ? ' is-active' : ''}`} aria-hidden="true" />
    <div>{url ? <a href={url} target="_blank" rel="noopener noreferrer">{item.title}</a> : <strong>{item.title}</strong>}
      <small>{m?.kind === 'workflow_job' ? `Job ${m.id}` : `Deployment ${m?.deploymentId ?? m?.id}`}{m?.environment ? ` · ${m.environment}` : ''}</small>
      {item.groupRelation?.startsWith('explicit job') && <small>{item.groupRelation.replace('explicit job/check deployment.id; job ', 'Job ')}</small>}
    </div><span className="activity-pipeline-state">{status?.replaceAll('_', ' ') || 'State unavailable'}</span>
  </li>;
}

function PipelineMembers({ post, query, view, now, onUnavailable }: { post: GithubActivityItem; query: string; view: 'events' | 'children'; now: number; onUnavailable: () => void }) {
  const { root, items, busy, error, hasMore, hasUpdates, loadMore, update, retry } = usePipelineMembers(post, query, view, now);
  return <div ref={root} className="activity-pipeline-members">
    {hasUpdates && <Button variant="ghost" size="sm" disabled={busy} onClick={update}>Update activity</Button>}
    {view === 'children' ? <ul className="activity-pipeline-children">{items.map(item => <PipelineChild key={item.id} item={item} />)}</ul> : <ol className="activity-pipeline-history">{items.map(item => <li key={item.id} data-activity-member-id={item.id}>
      <div><strong>{item.actionLabel}</strong><time dateTime={item.receivedAt}>{new Date(item.receivedAt).toLocaleString()}</time></div>
      <p>{item.title} · {item.reference}</p>
      {item.pipeline && <p>Source: {item.pipeline.updatedAt || item.pipeline.completedAt || item.pipeline.startedAt || item.pipeline.createdAt || 'Time unavailable'}</p>}
      <small>{item.groupRelation}</small>
      <ActivityDetails eventId={item.id} onUnavailable={onUnavailable} />
    </li>)}</ol>}
    {busy && <p role="status">Loading activity…</p>}
    {error && <p role="alert">{error} <Button variant="ghost" size="sm" onClick={retry}>Retry</Button></p>}
    {!busy && !error && !items.length && <p>No retained activity available.</p>}
    {!busy && !error && hasMore && <Button variant="ghost" size="sm" onClick={loadMore}>Load more</Button>}
  </div>;
}

export function GithubActivityCard({ post, query = '', project, saved, onBookmark, now, onUnavailable }: {
  post: GithubActivityItem; query?: string; project?: Project; saved: boolean; onBookmark: () => void; now: number; onUnavailable: (id: string) => void;
}) {
  const [activity, setActivity] = useState(false);
  const [allChildren, setAllChildren] = useState(false);
  const unavailable = useCallback(() => onUnavailable(post.id), [onUnavailable, post.id]);
  const EventIcon = post.icon === 'pipeline' && post.tone === 'success' ? CircleCheck : post.icon === 'pipeline' && post.tone === 'danger' ? CircleX : icons[post.icon] ?? CircleDot;
  const url = safeActivityUrl(post.targetUrl);
  const group = post.group;
  const sourceKey = `${post.id}:${query}`;
  const reference = <><div className="activity-reference-heading"><EventIcon aria-hidden="true" /><strong>{post.title}</strong>{post.result && <span className="activity-reference-result">{post.result}</span>}</div>
    <div className="activity-reference-meta">{post.branch && <><span className="activity-branch"><GitBranch aria-hidden="true" /><span>{post.baseBranch ? `${post.baseBranch} ← ${post.branch}` : post.branch}</span></span><span aria-hidden="true">·</span></>}<span className="activity-reference-id">{post.reference}</span></div></>;
  return <article className="activity-post" data-activity-id={post.id} aria-label={`${post.projectName}: ${post.actionLabel}`}>
    <div className="activity-post-layout"><span className="activity-avatar" aria-hidden="true">{project?.icon ? <img src={project.icon} alt="" /> : <span>{post.projectName.slice(0, 2).toUpperCase()}</span>}</span>
      <div className="activity-post-content">
        <div className="activity-byline"><strong>{post.projectName}</strong><span className="activity-provider">GitHub</span>{!post.retentionPending && <><span aria-hidden="true">·</span><time className="activity-time" dateTime={post.receivedAt} title={new Date(post.receivedAt).toLocaleString()}>{receivedTime(post.receivedAt, now)}</time></>}</div>
        <div className={`activity-event activity-tone--${post.tone}`}><EventIcon aria-hidden="true" /><span>{post.actionLabel}</span></div>
        <p className="activity-post-body">{post.retentionPending ? 'Activity needs refresh.' : post.summary}</p>
        {url ? <a className={`activity-reference activity-tone--${post.tone}`} href={url} target="_blank" rel="noopener noreferrer" aria-label={post.targetKind === 'repository' ? `Open repository: ${post.repository ?? post.projectName}` : `Open ${post.reference}: ${post.title}`}>{reference}</a> : <div className={`activity-reference activity-tone--${post.tone}`}>{reference}</div>}
        {group && <>
          {group.filtered && <p className="activity-pipeline-filtered">Filtered activity</p>}
          {!allChildren && group.children.length > 0 && <ul className="activity-pipeline-children">{group.children.map(item => <PipelineChild key={`${item.pipeline?.kind}:${item.pipeline?.id}`} item={item} />)}</ul>}
          {(group.childrenHasMore || allChildren) && <Button variant="ghost" size="sm" aria-expanded={allChildren} onClick={() => setAllChildren(value => !value)}>{allChildren ? 'Show fewer jobs and deployments' : post.retentionPending ? 'All jobs and deployments' : `All jobs and deployments (${group.childCount})`}</Button>}
          {allChildren && <PipelineMembers key={`children:${sourceKey}`} post={post} query={query} view="children" now={now} onUnavailable={unavailable} />}
        </>}
        <div className="activity-post-actions">{group ? <Button variant="ghost" size="sm" aria-expanded={activity} onClick={() => setActivity(value => !value)}><MessageSquare />{activity ? 'Hide activity' : post.retentionPending ? 'Activity' : `Activity (${group.eventCount})`}</Button> : <ActivityDetails key={post.detailEventId ?? post.id} eventId={post.detailEventId ?? post.id} onUnavailable={unavailable} />}
          <IconButton label={saved ? 'Remove bookmark' : 'Bookmark update'} disabled={post.retentionPending && !saved} aria-pressed={saved} className={saved ? 'is-saved' : ''} onClick={onBookmark}><Bookmark fill={saved ? 'currentColor' : 'none'} /></IconButton></div>
        {group && activity && <PipelineMembers key={`events:${sourceKey}`} post={post} query={query} view="events" now={now} onUnavailable={unavailable} />}
      </div>
    </div>
  </article>;
}
