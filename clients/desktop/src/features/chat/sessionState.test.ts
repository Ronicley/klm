import { describe, expect, it } from 'vitest';
import type { EngineEvent, SessionSummary, SessionUpdate } from '../../engine';
import { isSessionResponse, mergeSessions, prependHistory } from './sessionState';

const summary: SessionSummary = {
  id: 'session-1', projectId: 'project-1', title: 'Session', workspace: 'Ungrouped',
  harness: 'opencode', status: 'idle', createdAt: '2026-09-15T10:00:00Z', updatedAt: '2026-09-15T10:00:00Z',
};
const message = (id: string, text = id): EngineEvent => ({ id, text, type: 'assistant', createdAt: summary.createdAt });
const reset = (events: EngineEvent[] | null, startIndex = 0): SessionUpdate => ({
  kind: 'reset', revision: 1, summary, total: startIndex + (events?.length ?? 0), changes: [],
  history: { sessionId: summary.id, revision: 1, startIndex, endIndex: startIndex + (events?.length ?? 0), total: startIndex + (events?.length ?? 0), events, hasMore: startIndex > 0, nextCursor: startIndex ? events?.[0].id : undefined },
});

describe('session transport', () => {
  it('renders a summary with a safe event list and hydrates the SSE reset', () => {
    const initial = mergeSessions([], [summary]);
    expect(initial[0].events.length).toBe(0);
    expect(initial[0].history).toBeUndefined();
    const snapshot = reset([message('one')]);
    expect(isSessionResponse(snapshot)).toBe(true);
    const hydrated = mergeSessions(initial, [snapshot], true);
    expect(hydrated[0].events.map(event => event.id)).toEqual(['one']);
    expect(mergeSessions(hydrated, [{ ...summary, title: 'Renamed' }])[0].events).toEqual(hydrated[0].events);
  });

  it('accepts the engine null event page for a new empty session', () => {
    const [session] = mergeSessions([], [reset(null)], true);
    expect(session.events).toEqual([]);
    expect(session.history?.total).toBe(0);
  });

  it('replaces streaming text, appends new events, and ignores old replay', () => {
    const initial = mergeSessions([], [reset([message('one', 'partial')])], true);
    const update: SessionUpdate = {
      kind: 'delta', revision: 2, summary, total: 2,
      changes: [{ index: 0, revision: 2, event: message('one', 'complete') }, { index: 1, revision: 2, event: message('two') }],
    };
    const next = mergeSessions(initial, [update], true);
    expect(next[0].events.map(event => event.text)).toEqual(['complete', 'two']);
    expect(mergeSessions(next, [reset([message('one', 'old')])], true)[0].events).toEqual(next[0].events);
  });

  it('does not let an HTTP mutation skip intervening SSE events', () => {
    const initial = mergeSessions([], [reset([message('one')])], true);
    const mutation: SessionUpdate = { kind: 'delta', revision: 3, summary, total: 3, changes: [{ index: 2, revision: 3, event: message('three') }] };
    const afterHTTP = mergeSessions(initial, [mutation]);
    expect(afterHTTP[0].history?.revision).toBe(1);
    const second: SessionUpdate = { kind: 'delta', revision: 2, summary, total: 2, changes: [{ index: 1, revision: 2, event: message('two') }] };
    const afterStream = mergeSessions(afterHTTP, [second, mutation], true);
    expect(afterStream[0].events.map(event => event.id)).toEqual(['one', 'two', 'three']);
  });

  it('reconciles an HTTP append without advancing the live cursor or duplicating the SSE event', () => {
    const initial = mergeSessions([], [reset([message('one')])], true);
    const mutation: SessionUpdate = { kind: 'delta', revision: 2, summary, total: 2, changes: [{ index: 1, revision: 2, event: message('two') }] };
    const optimistic = mergeSessions(initial, [mutation], 'mutation');
    expect(optimistic[0].events.map(item => item.id)).toEqual(['one', 'two']);
    expect(optimistic[0].history?.revision).toBe(1);
    const afterSSE = mergeSessions(optimistic, [mutation], true);
    expect(afterSSE[0].events.map(item => item.id)).toEqual(['one', 'two']);
    expect(afterSSE[0].history?.revision).toBe(2);
  });

  it('discards potentially stale older pages when the server requires a reset', () => {
    const initial = mergeSessions([], [reset([message('one'), message('two'), message('three')])], true);
    const newer: SessionUpdate = { ...reset([message('two'), message('three')], 1), revision: 2 };
    const result = mergeSessions(initial, [newer], true);
    expect(result[0].events.map(item => item.id)).toEqual(['two', 'three']);
    expect(result[0].history?.startIndex).toBe(1);
  });

  it('does not roll back newer HTTP event content while consuming earlier SSE revisions', () => {
    const initial = mergeSessions([], [reset([message('one')])], true);
    const mutation: SessionUpdate = { kind: 'delta', revision: 3, summary, total: 1, changes: [{ index: 0, revision: 3, event: message('one', 'new') }] };
    const second: SessionUpdate = { kind: 'delta', revision: 2, summary, total: 1, changes: [{ index: 0, revision: 2, event: message('one', 'old') }] };
    const afterHTTP = mergeSessions(initial, [mutation], 'mutation');
    const afterSSE = mergeSessions(afterHTTP, [second], true);
    expect(afterSSE[0].events[0].text).toBe('new');
    expect(afterSSE[0].history?.revision).toBe(2);
  });

  it('prepends older messages without replacing the current streamed window', () => {
    const initial = mergeSessions([], [reset([message('latest')], 2)], true);
    const older = { sessionId: summary.id, revision: 1, startIndex: 0, endIndex: 2, total: 3, events: [message('one'), message('two')], hasMore: false };
    const next = prependHistory(initial, older);
    expect(next[0].events.map(event => event.id)).toEqual(['one', 'two', 'latest']);
    expect(next[0].history?.hasMore).toBe(false);
    expect(prependHistory(next, older)[0].events).toEqual(next[0].events);
  });

  it('keeps older pages on a resumed delta after hydration and pagination', () => {
    const initial = mergeSessions([], [reset([message('latest')], 2)], true);
    const page = { sessionId: summary.id, revision: 1, startIndex: 0, endIndex: 2, total: 3, events: [message('one'), message('two')], hasMore: false };
    const paginated = prependHistory(initial, page);
    const resumed: SessionUpdate = { kind: 'delta', revision: 2, summary, total: 4, changes: [{ index: 3, revision: 2, event: message('new') }] };
    const next = mergeSessions(paginated, [resumed], true);
    expect(next[0].events.map(event => event.id)).toEqual(['one', 'two', 'latest', 'new']);
    expect(next[0].history?.startIndex).toBe(0);
    expect(next[0].history?.revision).toBe(2);
  });
});
