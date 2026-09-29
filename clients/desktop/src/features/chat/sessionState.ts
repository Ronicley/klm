import { mergeGraphState, type EngineEvent, type EventPage, type Session, type SessionSummary, type SessionUpdate } from '../../engine';

// HTTP can deliver revision N before SSE delivers N-1. Track each loaded
// object's revision separately from the contiguous stream cursor.
const eventRevisions = new WeakMap<EngineEvent, number>();

function reconcileEvent(previous: EngineEvent | undefined, incoming: EngineEvent, revision: number): EngineEvent {
  if (previous?.id === incoming.id) {
    if ((eventRevisions.get(previous) ?? -1) > revision) return previous;
    if (JSON.stringify(previous) === JSON.stringify(incoming)) {
      eventRevisions.set(previous, revision);
      return previous;
    }
  }
  eventRevisions.set(incoming, revision);
  return incoming;
}

function sameSummary(previous: Session, summary: SessionSummary) {
  const { events: _events, history: _history, sources: _sources, graph: _graph, ...metadata } = previous;
  const { graph: _incomingGraph, ...incoming } = summary;
  return JSON.stringify(metadata) === JSON.stringify(incoming);
}

function version(timestamp: string) {
  const [seconds, fraction = ''] = timestamp.replace(/Z$/, '').split('.');
  return `${seconds}.${fraction.padEnd(9, '0')}`;
}

export function mergeSessions(current: Session[], incoming: (SessionSummary | Session | SessionUpdate)[], live: boolean | 'mutation' = false): Session[] {
  const sessions = new Map(current.map(session => [session.id, session]));
  let changed = false;
  for (const item of incoming) {
    const update = 'summary' in item ? item : undefined;
    const summary = update ? update.summary : item as SessionSummary | Session;
    if (summary.role === 'graph_node') continue;
    const previous = sessions.get(summary.id);
    const stale = update && previous?.history && update.revision < previous.history.revision;
    const base = previous && (stale || version(summary.updatedAt) < version(previous.updatedAt) ||
      (summary.updatedAt === previous.updatedAt && sameSummary(previous, summary))) ? previous : summary;
    const graph = mergeGraphState(previous?.graph, summary.graph);
    let events = previous?.events ?? [];
    let history = previous?.history;
    if (!update && 'events' in item && Array.isArray(item.events) && base === summary) {
      events = item.events;
      history = { revision: 0, startIndex: 0, total: events.length, hasMore: false };
    }
    // Mutation responses contain only that commit's changes. They must not
    // advance the SSE cursor or they could skip an intervening streamed event.
    if (live === true && update?.history && (!history || update.revision >= history.revision)) {
      const page = update.history;
      const previousById = new Map(events.map(event => [event.id, event]));
      // An expired journal can contain edits outside this page. Discard older
      // cached pages on reset; ordinary reconnect resumes by per-session cursor.
      events = (page.events ?? []).map(event => reconcileEvent(previousById.get(event.id), event, update.revision));
      history = { revision: update.revision, startIndex: page.startIndex, total: page.total, nextCursor: page.nextCursor, hasMore: page.hasMore };
    } else if (update && history && update.revision >= history.revision) {
      let nextEvents = events;
      // The SSE reset supplies the initial window; deltas replace or append by
      // absolute index, including edits to an event already being streamed.
      for (const change of update.changes) {
        if (change.index < history.startIndex) continue;
        const index = change.index - history.startIndex;
        // Mutation responses can arrive before earlier SSE revisions. Only
        // overlay already loaded IDs or the immediate append; never skip gaps.
        if (live !== true && index < nextEvents.length && nextEvents[index].id !== change.event.id) continue;
        if (index > nextEvents.length) continue;
        const event = reconcileEvent(nextEvents[index], change.event, change.revision);
        if (nextEvents[index] !== event) {
          if (nextEvents === events) nextEvents = events.slice();
          nextEvents[index] = event;
        }
      }
      events = nextEvents;
      if (live === true && (update.revision !== history.revision || update.total !== history.total)) history = { ...history, revision: update.revision, total: update.total };
    }
    const next = {
      ...base, events, history,
      runtimeActive: stale ? previous?.runtimeActive : summary.runtimeActive,
      ...(graph ? { graph, selectedGraphId: graph.selectedGraphId } : {}),
    };
    if (previous && base === previous && events === previous.events && history === previous.history && graph === previous.graph) continue;
    sessions.set(summary.id, next);
    changed = true;
  }
  return changed ? [...sessions.values()] : current;
}

export function prependHistory(sessions: Session[], page: EventPage): Session[] {
  return sessions.map(session => {
    const history = session.history;
    if (session.id !== page.sessionId || !history || page.endIndex !== history.startIndex) return session;
    return {
      ...session,
      events: [...(page.events ?? []).map(event => reconcileEvent(undefined, event, page.revision)), ...session.events],
      history: { ...history, startIndex: page.startIndex, nextCursor: page.nextCursor, hasMore: page.hasMore },
    };
  });
}

export function isSessionResponse(value: unknown): value is Session | SessionUpdate {
  if (!value || typeof value !== 'object') return false;
  if ('summary' in value) {
    const update = value as SessionUpdate;
    return (update.kind === 'reset' || update.kind === 'delta') && typeof update.summary?.id === 'string'
      && typeof update.summary.updatedAt === 'string' && Number.isSafeInteger(update.revision)
      && Array.isArray(update.changes) && (update.kind !== 'reset' || !!update.history);
  }
  const session = value as Session;
  return typeof session.id === 'string' && typeof session.updatedAt === 'string' && Array.isArray(session.events);
}
