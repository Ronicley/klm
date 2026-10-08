import type { ChatFile, FileMention, MentionPreparation } from '../../engine';

export type Message = { id: string; role: 'user' | 'assistant'; text: string; status?: string; favorite?: boolean; context?: string[]; thought?: string; mentions?: FileMention[]; mentionPreparation?: MentionPreparation[]; files?: ChatFile[] };
export type Session = { id: string; projectId: string; title: string; workspace: string; messages: Message[] };

export const initialSessions: Session[] = [
  { id: 'demo', projectId: 'demo-project', title: 'Hi', workspace: 'demo', messages: [
    { id: 'user-1', role: 'user', text: 'hi' },
    { id: 'agent-1', role: 'assistant', context: ['AGENTS.md', '@deepseek-ai/dsh-system-prompt', 'skill-catalog'], thought: 'The user just said "hi". The workspace instruction says to answer in English. I should respond in English and ask what they would like to work on.', text: "Hi! I'm your coding agent, ready to help. What would you like to work on?" },
  ] },
  { id: 'ungrouped', projectId: 'demo-project', title: 'Hi', workspace: 'Ungrouped', messages: [{ id: 'user-2', role: 'user', text: 'hi' }] },
];
