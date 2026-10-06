import { afterEach, expect, it, vi } from 'vitest';
import { connectUpdates } from './updatesConnection';

afterEach(() => vi.useRealTimers());

it('resumes the hydrated and paginated window, replaces subscriptions and cleans up retries', () => {
  vi.useFakeTimers();
  const opened: { url: string; source: EventSource & { closed: boolean } }[] = [];
  const create = (url: string) => {
    const source = { closed: false, close() { source.closed = true; }, onerror: null, onopen: null, onmessage: null } as unknown as EventSource & { closed: boolean };
    opened.push({ url, source });
    return source;
  };
  const cursor = { first: undefined as number | undefined, second: undefined as number | undefined };
  const cursors = () => Object.fromEntries(Object.entries(cursor).filter((entry): entry is [string, number] => entry[1] !== undefined));
  const onConnection = vi.fn();
  const disconnect = vi.fn();
  const start = (ids: string[]) => connectUpdates('http://localhost:7331', ids, cursors, () => {}, disconnect, onConnection, create);
  const disposeFirst = start(['first']);
  expect(JSON.parse(new URL(opened[0].url).searchParams.get('cursors')!)).toEqual({});
  // A reset hydrates first; loading older pages changes its window, not its
  // revision. A subsequent delta advances the cursor without discarding pages.
  cursor.first = 12;
  opened[0].source.onerror?.(new Event('error'));
  expect(opened[0].source.closed).toBe(true);
  vi.advanceTimersByTime(1000);
  expect(JSON.parse(new URL(opened[1].url).searchParams.get('cursors')!)).toEqual({ first: 12 });
  disposeFirst();
  expect(opened[1].source.closed).toBe(true);
  const disposeSecond = start(['second']);
  expect(new URL(opened[2].url).searchParams.get('ids')).toBe('second');
  cursor.second = 17;
  opened[2].source.onerror?.(new Event('error'));
  disposeSecond();
  vi.advanceTimersByTime(30000);
  expect(opened).toHaveLength(3);
  expect(onConnection).toHaveBeenLastCalledWith(null);
  expect(disconnect).toHaveBeenCalledTimes(2);
});
