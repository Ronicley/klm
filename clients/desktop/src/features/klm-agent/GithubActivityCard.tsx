import { useEffect, useState } from 'react';
import { Bookmark, CircleCheck, CircleDot, CircleX, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, MessageSquare, Radio, ShieldAlert } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { EngineRequestError, type Project } from '../../engine';
import { getActivityDetails, safeActivityUrl, type GithubActivityItem, type GithubActivityDetails } from './githubActivityApi';

const icons: Record<string, typeof GitPullRequest> = { pr: GitPullRequest, merge: GitMerge, issue: CircleDot, comment: MessageSquare, commit: GitCommitHorizontal, security: ShieldAlert, pipeline: Radio, event: CircleDot };
function receivedTime(stamp: string, now: number) {
  const seconds = Math.max(0, Math.floor((now - Date.parse(stamp)) / 1000));
  if (seconds < 60) return 'Just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}
export function GithubActivityCard({ post, project, saved, onBookmark, now, onUnavailable }: {
  post: GithubActivityItem; project?: Project; saved: boolean; onBookmark: () => void; now: number; onUnavailable: (id: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [details, setDetails] = useState<GithubActivityDetails>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!expanded || details) return;
    const controller = new AbortController(); setError('');
    void getActivityDetails(post.id, controller.signal).then(value => { if (!controller.signal.aborted) setDetails(value); }).catch(cause => {
      if (controller.signal.aborted) return;
      if (cause instanceof EngineRequestError && cause.status === 404) onUnavailable(post.id);
      else setError(cause instanceof Error ? cause.message : 'Details could not be loaded.');
    });
    return () => controller.abort();
  }, [expanded, details, post.id, attempt, onUnavailable]);
  const EventIcon = post.icon === 'pipeline' && post.tone === 'success' ? CircleCheck : post.icon === 'pipeline' && post.tone === 'danger' ? CircleX : icons[post.icon] ?? CircleDot;
  const url = safeActivityUrl(post.targetUrl);
  const reference = <><div className="activity-reference-heading"><EventIcon aria-hidden="true" /><strong>{post.title}</strong>{post.result && <span className="activity-reference-result">{post.result}</span>}</div>
    <div className="activity-reference-meta">{post.branch && <><span className="activity-branch"><GitBranch aria-hidden="true" /><span>{post.baseBranch ? `${post.baseBranch} ← ${post.branch}` : post.branch}</span></span><span aria-hidden="true">·</span></>}<span className="activity-reference-id">{post.reference}</span></div></>;
  return <article className="activity-post" data-activity-id={post.id} aria-label={`${post.projectName}: ${post.actionLabel}`}>
    <div className="activity-post-layout"><span className="activity-avatar" aria-hidden="true">{project?.icon ? <img src={project.icon} alt="" /> : <span>{post.projectName.slice(0, 2).toUpperCase()}</span>}</span>
      <div className="activity-post-content">
        <div className="activity-byline"><strong>{post.projectName}</strong><span className="activity-provider">GitHub</span><span aria-hidden="true">·</span><time className="activity-time" dateTime={post.receivedAt} title={new Date(post.receivedAt).toLocaleString()}>{receivedTime(post.receivedAt, now)}</time></div>
        <div className={`activity-event activity-tone--${post.tone}`}><EventIcon aria-hidden="true" /><span>{post.actionLabel}</span></div>
        <p className="activity-post-body">{post.summary}</p>
        {url ? <a className={`activity-reference activity-tone--${post.tone}`} href={url} target="_blank" rel="noopener noreferrer" aria-label={post.targetKind === 'repository' ? `Open repository: ${post.repository ?? post.projectName}` : `Open ${post.reference}: ${post.title}`}>{reference}</a> : <div className={`activity-reference activity-tone--${post.tone}`}>{reference}</div>}
        <div className="activity-post-actions"><Button variant="ghost" size="sm" aria-expanded={expanded} aria-controls={`activity-detail-${post.id}`} onClick={() => setExpanded(value => !value)}><MessageSquare />{expanded ? 'Hide details' : 'Details'}</Button><IconButton label={saved ? 'Remove bookmark' : 'Bookmark update'} aria-pressed={saved} className={saved ? 'is-saved' : ''} onClick={onBookmark}><Bookmark fill={saved ? 'currentColor' : 'none'} /></IconButton></div>
        {expanded && <div id={`activity-detail-${post.id}`} className="activity-detail">
          {error ? <p role="alert">{error} <Button size="sm" variant="ghost" onClick={() => setAttempt(value => value + 1)}>Retry</Button></p> : !details ? <p role="status">Loading details…</p> : <>
            <dl>{details.fields.map((field, index) => <div key={index}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}</dl>
            {details.body && <p>{details.body}</p>}
            {details.entries && <ul>{details.entries.map((entry, index) => <li key={index}>{safeActivityUrl(entry.url) ? <a href={safeActivityUrl(entry.url)} target="_blank" rel="noopener noreferrer">{entry.text}</a> : entry.text}</li>)}</ul>}
            {details.truncated && <p>{url ? 'Details truncated. Open on GitHub.' : 'Details truncated.'}</p>}
          </>}
        </div>}
      </div>
    </div>
  </article>;
}
