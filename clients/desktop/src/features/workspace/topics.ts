import { compareRevision, type Project, type Session, type Topic } from '../../engine';

export const topicColors: Topic['color'][] = ['neutral', 'blue', 'cyan', 'teal', 'green', 'purple', 'rose', 'red', 'orange', 'amber'];
export const topicKey = (session: Session) => session.topicId ?? '';
export const sessionTopic = (project: Project | undefined, session: Session | undefined) => {
  const listed = project?.topics?.find(topic => topic.id === session?.topicId);
  const exact = session?.topic;
  if (!exact || exact.id !== session?.topicId) return listed;
  return listed && compareRevision(listed.revision ?? 0, exact.revision ?? 0) >= 0 ? listed : exact;
};
export const topicArchived = (project: Project | undefined, session: Session | undefined) => !!sessionTopic(project, session)?.archived;

// Engine timestamps are UTC RFC3339Nano. Keep sub-millisecond precision rather
// than treating distinct creation times as ties through JavaScript Date.parse.
function creationKey(value = '') {
  if (!value) return '';
  const [seconds, fraction = ''] = value.replace(/Z$/, '').split('.');
  return `${seconds}.${fraction.padEnd(9, '0')}`;
}

function newestCreationFirst(a?: Session, b?: Session) {
  const left = creationKey(a?.createdAt), right = creationKey(b?.createdAt);
  return left < right ? 1 : left > right ? -1 : 0;
}

export function newestSessionFirst(a: Session, b: Session) {
  return newestCreationFirst(a, b) || a.id.localeCompare(b.id);
}

// All persisted members determine a group's position, including archived ones.
export function topicGroups(project: Project, sessions: Session[]) {
  const topics = new Map((project.topics ?? []).map(topic => [topic.id, topic]));
  for (const session of sessions) { const topic = sessionTopic(project, session); if (topic) topics.set(topic.id, topic); }
  const groups = [...topics.values()].map(topic => ({ id: topic.id, name: topic.name, topic, sessions: sessions.filter(session => session.topicId === topic.id).sort(newestSessionFirst) }));
  const positions = new Map((project.topicOrder ?? []).map((id, index) => [id, index]));
  groups.sort((a, b) => {
    if (a.topic.order !== undefined && b.topic.order !== undefined) return a.topic.order - b.topic.order || a.id.localeCompare(b.id);
    const left = positions.get(a.id), right = positions.get(b.id);
    if (left !== undefined || right !== undefined) return left === undefined ? -1 : right === undefined ? 1 : left - right;
    return newestCreationFirst(a.sessions[0], b.sessions[0]) || a.id.localeCompare(b.id);
  });
  return [...groups, { id: '', name: 'Ungrouped', topic: undefined, sessions: sessions.filter(session => !session.topicId).sort(newestSessionFirst) }];
}
