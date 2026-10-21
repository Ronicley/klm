import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useRef } from 'react';
import { GithubActivityCard } from './GithubActivityCard';
import { getActivityBookmarks, getActivityCatalog, getActivityDetails, getActivityPage, type GithubActivityItem, type GithubActivityPage } from './githubActivityApi';
import { useActivityBookmarks } from './useActivityBookmarks';
import { useGithubActivityFeed } from './useGithubActivityFeed';

vi.mock('./githubActivityApi', async importOriginal => ({ ...await importOriginal<typeof import('./githubActivityApi')>(), getActivityPage: vi.fn(), getActivityDetails: vi.fn(), getActivityBookmarks: vi.fn(), getActivityCatalog: vi.fn() }));
afterEach(() => { cleanup(); vi.useRealTimers(); vi.resetAllMocks(); });
const now = Date.parse('2026-10-16T12:00:00Z');
const item = (seq: number): GithubActivityItem => ({ id: `event-${seq}`, detailEventId: `event-${seq}`, projectId: 'p', projectName: 'Project', webhookId: 'w', provider: 'github', eventType: 'workflow_run', releaseSequence: String(seq), receivedAt: new Date(now).toISOString(), emittedAt: '', category: 'Pipelines', tone: 'success', icon: 'pipeline', actionLabel: 'Workflow completed', summary: 'Summary', title: `Transition ${seq}`, reference: 'Run #42' });
const card = (seq = 30, composition = 'initial'): GithubActivityItem => ({ ...item(seq), id: 'run-card', title: 'Workflow', group: { kind: 'workflow', runId: '42', attempt: '1', eventCount: 21, childCount: 0, children: [], childrenHasMore: false, filtered: false, highWatermark: String(seq), composition } });
let events: GithubActivityItem[];
beforeEach(() => {
  events = Array.from({ length: 21 }, (_, index) => item(30 - index));
  vi.mocked(getActivityPage).mockImplementation(async (query, cursor) => {
    const high = new URLSearchParams(query).get('high')!;
    const eligible = events.filter(event => Number(event.releaseSequence) <= Number(high));
    const offset = cursor ? 20 : 0;
    return { items: eligible.slice(offset, offset + 20), hasMore: eligible.length > offset + 20, nextCursor: eligible.length > offset + 20 ? 'older' : undefined, highWatermark: high, asOf: new Date(now).toISOString() } satisfies GithubActivityPage;
  });
  vi.mocked(getActivityDetails).mockImplementation(async id => ({ id, fields: [], body: `Details for ${id}`, truncated: false }));
});
const props = { saved: false, onBookmark: vi.fn(), onUnavailable: vi.fn(), now };

it('keeps page two and open Details across transitions, admitting new events only explicitly', async () => {
  const view = render(<GithubActivityCard {...props} post={card()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Activity (21)' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  await screen.findByText('Transition 10 · Run #42');
  fireEvent.click(screen.getAllByRole('button', { name: 'Details' }).at(-1)!);
  await screen.findByText('Details for event-10');
  events = [item(31), ...events];
  view.rerender(<GithubActivityCard {...props} post={card(31, 'added')} />);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Update activity' })).toBeEnabled());
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
  expect(screen.queryByText('Transition 31 · Run #42')).not.toBeInTheDocument();
  expect(vi.mocked(getActivityPage).mock.calls.at(-1)?.[0]).toContain('high=30');
  fireEvent.click(screen.getByRole('button', { name: 'Update activity' }));
  await screen.findByText('Transition 31 · Run #42');
  expect(screen.getByText('Transition 10 · Run #42')).toBeInTheDocument();
});

it('removes expired/removed rows and their Details when composition changes without a new release', async () => {
  events[20].expiresAt = new Date(now + 1000).toISOString();
  const view = render(<GithubActivityCard {...props} post={card()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Activity (21)' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  await screen.findByText('Transition 10 · Run #42');
  fireEvent.click(screen.getAllByRole('button', { name: 'Details' }).at(-1)!);
  await screen.findByText('Details for event-10');
  view.rerender(<GithubActivityCard {...props} now={now + 1500} post={card()} />);
  expect(screen.queryByText('Details for event-10')).not.toBeInTheDocument();
  events = events.filter(event => event.id !== 'event-10' && event.id !== 'event-11');
  const next = card(30, 'removed'); next.group!.eventCount = 19;
  view.rerender(<GithubActivityCard {...props} now={now + 1500} post={next} />);
  expect(screen.queryByText('Details for event-10')).not.toBeInTheDocument();
  await waitFor(() => expect(screen.queryByText('Transition 11 · Run #42')).not.toBeInTheDocument());
  expect(screen.getByText('Transition 30 · Run #42')).toBeInTheDocument();
});

it.each(['deployment-card', 'check-event'])('keeps an in-memory bookmark for %s after late correlation and removes it on source deletion', async id => {
  const post = { ...item(1), id, detailEventId: 'original-event' };
  let target = id;
  vi.mocked(getActivityBookmarks).mockImplementation(async ids => ({ items: target ? ids.map(eventId => ({ eventId, cardId: target })) : [] }));
  const hook = renderHook(({ revision }) => useActivityBookmarks(revision, now, '30'), { initialProps: { revision: 'first' } });
  act(() => hook.result.current.toggle(post));
  await waitFor(() => expect(getActivityBookmarks).toHaveBeenCalled());
  target = 'run-card'; hook.rerender({ revision: 'correlated' });
  await waitFor(() => expect(hook.result.current.saved(card())).toBe(true));
  expect(getActivityBookmarks).toHaveBeenLastCalledWith(['original-event'], '30', expect.any(AbortSignal));
  target = ''; hook.rerender({ revision: 'deleted' });
  await waitFor(() => expect(hook.result.current.saved(card())).toBe(false));
});

it('revalidates a group at member expiry without dropping its surviving card from the feed', async () => {
  const start = Date.now();
  vi.useFakeTimers();
  vi.setSystemTime(start);
  const initial = card();
  initial.expiresAt = new Date(start + 1000).toISOString();
  const remaining = card(30, 'old-member-expired');
  remaining.group!.eventCount = 20;
  remaining.expiresAt = new Date(start + 60000).toISOString();
  vi.mocked(getActivityCatalog).mockResolvedValue({ version: 1, items: [] });
  vi.mocked(getActivityPage).mockImplementation(async () => ({
    items: [Date.now() >= start + 1000 ? remaining : initial],
    hasMore: false, highWatermark: '30', asOf: new Date().toISOString(),
  }));
  const projects = [{ id: 'p', name: 'Project', folder: '', icon: '', folders: [], archivedFolders: [] }];
  const timeline = { current: null };
  const hook = renderHook(() => useGithubActivityFeed('All', '', projects, true, timeline));
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(hook.result.current.items).toHaveLength(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  expect(hook.result.current.items.map(post => post.id)).toEqual(['run-card']);
  expect(hook.result.current.items[0].group!.eventCount).toBe(20);
});

const retentionProjects = [{ id: 'p', name: 'Project', folder: '', icon: '', folders: [], archivedFolders: [] }];
function RetentionFeed() {
  const timeline = useRef<HTMLDivElement>(null);
  const feed = useGithubActivityFeed('All', '', retentionProjects, true, timeline);
  return <div ref={timeline} className="activity-timeline">
    {feed.error && <p role="alert">{feed.error}<button onClick={feed.retry}>Retry feed</button></p>}
    {feed.items.map(post => <GithubActivityCard key={post.id} {...props} post={post} query={feed.query} now={feed.clock} onUnavailable={feed.unavailable} />)}
  </div>;
}

function retentionFixture() {
  vi.useFakeTimers(); vi.setSystemTime(now);
  events = events.map((event, index) => ({ ...event, expiresAt: new Date(now + (index === 0 ? 1000 : 3000)).toISOString() }));
  const initial = { ...card(), title: 'Expiring group heading', summary: 'Expiring group summary', expiresAt: new Date(now + 1000).toISOString() };
  initial.group!.children = [{ ...item(40), title: 'Expiring preview', expiresAt: initial.expiresAt, pipeline: { kind: 'workflow_job', id: '40', repositoryId: 'r' } }];
  initial.group!.childCount = 1;
  const remaining = { ...card(30, 'member-expired'), title: 'Surviving workflow', expiresAt: new Date(now + 3000).toISOString() };
  remaining.group!.eventCount = 20;
  vi.mocked(getActivityCatalog).mockResolvedValue({ version: 1, items: [] });
  const read = async (query: string, cursor?: string): Promise<GithubActivityPage> => {
    const memberView = new URLSearchParams(query).get('view');
    const live = events.filter(event => Date.parse(event.expiresAt!) > Date.now());
    const offset = cursor ? 20 : 0;
    const items = memberView ? live.slice(offset, offset + 20) : Date.now() < now + 1000 ? [initial] : live.length ? [remaining] : [];
    const hasMore = !!memberView && live.length > offset + 20;
    return { items, hasMore, nextCursor: hasMore ? 'older' : undefined, highWatermark: '30', asOf: new Date().toISOString() };
  };
  vi.mocked(getActivityPage).mockImplementation(read);
  return read;
}
const flush = async (time = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(time); }); };
async function openRetainedDetails() {
  fireEvent.click(screen.getByRole('button', { name: 'Activity (21)' })); await flush();
  fireEvent.click(screen.getByRole('button', { name: 'Load more' })); await flush();
  fireEvent.click(screen.getAllByRole('button', { name: 'Details' }).at(-1)!); await flush();
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
}

it('preserves the mounted reader, loaded second page and surviving Details through partial expiry, then removes the empty group', async () => {
  retentionFixture();
  render(<RetentionFeed />); await flush();
  await openRetainedDetails();
  const article = screen.getByRole('article');
  const survivor = screen.getByText('Transition 10 · Run #42').closest('li');
  await flush(1500);
  expect(screen.getByRole('article')).toBe(article);
  expect(screen.getByRole('button', { name: 'Hide activity' })).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('Transition 10 · Run #42').closest('li')).toBe(survivor);
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
  expect(screen.queryByText('Transition 30 · Run #42')).not.toBeInTheDocument();
  expect(screen.queryByText('Expiring group heading')).not.toBeInTheDocument();
  expect(screen.getByText('Surviving workflow')).toBeInTheDocument();
  await flush(2000);
  expect(screen.queryByRole('article')).not.toBeInTheDocument();
  expect(screen.queryByText('Details for event-10')).not.toBeInTheDocument();
});

it('scrubs expired snippets while revalidation is pending or fails, and retries without discarding retained readers', async () => {
  const read = retentionFixture();
  let reject!: (error: Error) => void;
  let deferred = false;
  vi.mocked(getActivityPage).mockImplementation((query, cursor) => {
    if (!new URLSearchParams(query).has('view') && Date.now() >= now + 1000 && !deferred) {
      deferred = true; return new Promise((_, fail) => { reject = fail; });
    }
    return read(query, cursor);
  });
  render(<RetentionFeed />); await flush();
  await openRetainedDetails();
  const article = screen.getByRole('article');
  await flush(1000);
  expect(screen.getByRole('article')).toBe(article);
  expect(screen.queryByText('Expiring group heading')).not.toBeInTheDocument();
  expect(screen.queryByText('Expiring group summary')).not.toBeInTheDocument();
  expect(screen.queryByText('Expiring preview')).not.toBeInTheDocument();
  expect(screen.queryByText('Transition 30 · Run #42')).not.toBeInTheDocument();
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
  await act(async () => { reject(new Error('Offline')); });
  expect(screen.getByRole('alert')).toHaveTextContent('Offline');
  expect(screen.getByText('Activity needs refresh.')).toBeInTheDocument();
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry feed' })); await flush();
  expect(screen.getByRole('article')).toBe(article);
  expect(screen.getByText('Surviving workflow')).toBeInTheDocument();
  expect(screen.queryByText('Activity needs refresh.')).not.toBeInTheDocument();
  expect(screen.getByText('Details for event-10')).toBeInTheDocument();
});
