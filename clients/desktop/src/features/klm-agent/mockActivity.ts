// Layout fixtures only. These events do not describe actual project activity.
export type ActivityKind = 'pr-opened' | 'pr-merged' | 'issue-assigned' | 'pipeline-succeeded' | 'pipeline-failed' | 'attention' | 'pipeline-running' | 'agent-pr' | 'agent-review' | 'agent-push';
export type ActivityCategory = 'PRs' | 'Pipelines' | 'Agents' | 'Issues';
export type ActivityTone = 'accent' | 'success' | 'danger' | 'warning' | 'muted';

export type ActivityPost = {
  id: string;
  kind: ActivityKind;
  project: number;
  actor: string;
  time: string;
  title: string;
  body: string;
  reference: string;
  branch?: string;
  detail: string;
  progress?: number;
};

export const activityKinds: Record<ActivityKind, { label: string; categories: ActivityCategory[]; tone: ActivityTone }> = {
  'pr-opened': { label: 'Pull request opened', categories: ['PRs'], tone: 'success' },
  'pr-merged': { label: 'Pull request merged', categories: ['PRs'], tone: 'accent' },
  'issue-assigned': { label: 'Issue assigned', categories: ['Issues'], tone: 'accent' },
  'pipeline-succeeded': { label: 'Pipeline succeeded', categories: ['Pipelines'], tone: 'success' },
  'pipeline-failed': { label: 'Pipeline failed', categories: ['Pipelines'], tone: 'danger' },
  attention: { label: 'Needs attention', categories: ['Agents'], tone: 'warning' },
  'pipeline-running': { label: 'Pipeline running', categories: ['Pipelines'], tone: 'accent' },
  'agent-pr': { label: 'Agent published a PR', categories: ['PRs', 'Agents'], tone: 'success' },
  'agent-review': { label: 'Agent published a review', categories: ['PRs', 'Agents'], tone: 'accent' },
  'agent-push': { label: 'Agent pushed commits', categories: ['Agents'], tone: 'muted' },
};

export const mockActivity: ActivityPost[] = [
  { id: 'pr-284', kind: 'agent-pr', project: 0, actor: 'Implementation agent', time: '2m', title: 'Reuse completed subagents', body: 'The handoff is ready. Completed subagents can now pick up where they left off, with their context intact.', reference: 'PR #284', branch: 'feature/subagent-reuse', detail: 'Opened by Implementation agent · 4 files changed\nAdds session reuse, preserves previous messages, and updates the conversation tabs.' },
  { id: 'build-1294', kind: 'pipeline-succeeded', project: 0, actor: 'Build pipeline', time: '8m', title: 'Desktop build passed', body: 'Windows artifacts are ready. All checks passed in 2m 14s.', reference: 'Run #1294', branch: 'main', detail: 'Install dependencies → Type check → Build → Package\n4 of 4 steps completed. Windows installer artifact is ready.' },
  { id: 'attention-auth', kind: 'attention', project: 1, actor: 'Implementation agent', time: '12m', title: 'A decision before moving forward', body: 'The sign-in flow is ready for review. Should expired sessions return to the last page or the project home?', reference: 'Authentication', detail: 'Waiting for your input · Implementation agent\nThe two navigation paths are prepared. A decision is needed before the agent can finish the change.' },
  { id: 'run-1295', kind: 'pipeline-running', project: 0, actor: 'Release pipeline', time: '18m', title: 'Release validation is underway', body: 'Build and type checks are done. Running the Windows regression checks next.', reference: 'Run #1295', branch: 'release/0.3.0', progress: 68, detail: 'Build completed · Type checks completed\nWindows regression checks in progress · Packaging pending' },
  { id: 'review-96', kind: 'agent-review', project: 1, actor: 'Review agent', time: '24m', title: 'A fresh pair of eyes on the sign-in flow', body: '“The recovery path looks good. Left two suggestions to simplify token refresh and keep the error states consistent.”', reference: 'PR #96', detail: 'Review published · 2 comments\nSuggestions: consolidate the refresh guard and reuse the existing inline error component.' },
  { id: 'failed-1293', kind: 'pipeline-failed', project: 2, actor: 'Test pipeline', time: '32m', title: 'Regression checks failed on Windows', body: 'Three checks failed in the focus-mode suite. The failure report is ready to inspect.', reference: 'Run #1293', branch: 'fix/focus-window', detail: 'Failed step: Windows regression checks\n3 failures · 42 passed · Duration 1m 48s\nFocus restoration did not preserve the previous window bounds.' },
  { id: 'merged-281', kind: 'pr-merged', project: 0, actor: 'KLM Agent', time: '46m', title: 'One less thing between you and your work', body: 'Focus window improvements have landed, including remembered dimensions and smoother resizing.', reference: 'PR #281', branch: 'main', detail: 'Merged into main · 6 files changed\nIncludes window geometry, resize handles, and updated documentation.' },
  { id: 'opened-98', kind: 'pr-opened', project: 1, actor: 'GitHub', time: '1h', title: 'Add project-level notification preferences', body: 'A new pull request adds controls for which updates appear in your activity feed.', reference: 'PR #98', branch: 'feature/notifications', detail: 'Ready for review · 3 files changed\nAdds notification preferences and a project-level settings entry.' },
  { id: 'push-commits', kind: 'agent-push', project: 2, actor: 'Implementation agent', time: '1h', title: 'Small fixes, pushed and ready', body: 'Pushed 3 commits covering keyboard navigation, empty states, and a cleaner project picker.', reference: '3 commits', branch: 'improve/navigation', detail: 'e3f4a2d · Fix keyboard navigation\n7b2c910 · Refine empty states\na91d02f · Simplify project picker' },
  { id: 'issue-987', kind: 'issue-assigned', project: 0, actor: 'GitHub', time: '2h', title: 'Investigate intermittent focus restoration', body: 'Issue #987 was assigned to the implementation agent and is ready to be picked up.', reference: 'Issue #987', detail: 'Assigned to Implementation agent · Priority: normal\nReproduce the intermittent focus restoration issue after closing and reopening the desktop window.' },
];
