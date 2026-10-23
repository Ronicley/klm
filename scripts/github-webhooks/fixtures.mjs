// Partial synthetic examples, not a complete GitHub schema corpus. Sources and
// availability caveats are in GITHUB_ACTIVITY_FEED_IMPLEMENTATION_PLAN.md §3/11.
export const families = [
  ['issue_relates_to', 'relates_to_added'], ['create', ''], ['delete', ''],
  ['branch_protection_configuration', 'enabled'], ['branch_protection_rule', 'created'],
  ['push_ruleset_bypass', 'created'], ['bypass_request_secret_scanning', 'created'],
  ['check_run', 'completed'], ['check_suite', 'completed'], ['code_scanning_alert', 'created'],
  ['member', 'added'], ['commit_comment', 'created'], ['dependabot_alert', 'created'],
  ['deploy_key', 'created'], ['deployment_status', 'created'], ['deployment', 'created'],
  ['discussion_comment', 'created'], ['discussion', 'created'],
  ['dismissal_request_dependabot', 'created'], ['dismissal_request_code_scanning', 'created'], ['dismissal_request_secret_scanning', 'created'],
  ['fork', ''], ['issue_comment', 'created'], ['issue_dependencies', 'blocked_by_added'], ['issues', 'opened'],
  ['label', 'created'], ['merge_group', 'checks_requested'], ['meta', 'deleted'], ['milestone', 'created'],
  ['package', 'published'], ['page_build', ''], ['pull_request_review_comment', 'created'],
  ['pull_request_review_thread', 'resolved'], ['pull_request_review', 'submitted'], ['pull_request', 'opened'],
  ['push', ''], ['registry_package', 'published'], ['release', 'published'], ['repository', 'edited'],
  ['repository_advisory', 'published'], ['repository_import', ''], ['repository_ruleset', 'created'],
  ['repository_vulnerability_alert', 'create'], ['secret_scanning_alert_location', 'created'], ['secret_scanning_alert', 'created'],
  ['secret_scanning_scan', ''], ['security_and_analysis', ''], ['star', 'created'], ['status', ''],
  ['sub_issues', 'sub_issue_added'], ['team_add', ''], ['public', ''], ['watch', 'started'], ['gollum', ''],
  ['workflow_job', 'completed'], ['workflow_run', 'completed'],
];
const scenario = (name, catalogId, action, variant = '') => ({
  name, catalogId, action, variant,
  event: catalogId === 'push_ruleset_bypass' ? 'klm_simulated_push_ruleset_bypass' : catalogId,
  limitation: catalogId === 'push_ruleset_bypass' ? 'SYNTHETIC HEADER; official request_type discriminator, canonical header unconfirmed' : 'partial synthetic payload',
});
export const fixtures = families.map(([id, action]) => scenario(id.replaceAll('_', '-'), id, action));
const add = (name, id, action, variant) => fixtures.push(scenario(name, id, action, variant));
for (const action of ['opened', 'ready_for_review', 'synchronize', 'reopened', 'future_action']) add(`pr-${action.replaceAll('_', '-')}`, 'pull_request', action);
add('pr-merged', 'pull_request', 'closed', 'merged'); add('pr-closed', 'pull_request', 'closed', 'closed');
for (const action of ['assigned', 'closed']) add(`issue-${action}`, 'issues', action);
add('comment-pr', 'issue_comment', 'created', 'pr');
for (const action of ['edited', 'deleted']) add(`comment-${action}`, 'issue_comment', action);
add('related-cross-repo-omitted', 'issue_relates_to', 'relates_to_added', 'omitted');
for (const state of ['approved', 'changes_requested', 'commented']) add(`review-${state.replaceAll('_', '-')}`, 'pull_request_review', 'submitted', state);
add('thread-unresolved', 'pull_request_review_thread', 'unresolved');
for (const id of ['workflow_run', 'workflow_job']) {
  const prefix = id === 'workflow_run' ? 'workflow' : 'job';
  for (const action of (id === 'workflow_run' ? ['requested', 'in_progress'] : ['queued', 'waiting', 'in_progress'])) add(`${prefix}-${action.replaceAll('_', '-')}`, id, action, action);
  for (const state of ['success', 'failure', 'cancelled', 'skipped', 'null']) add(`${prefix}-${state}`, id, 'completed', state);
}
for (const id of ['check_run', 'check_suite']) {
  for (const state of ['success', 'failure']) add(`${id.replaceAll('_', '-')}-${state}`, id, 'completed', state);
  add(`${id.replaceAll('_', '-')}-pending`, id, id === 'check_run' ? 'created' : 'requested', 'queued');
}
for (const state of ['pending', 'success', 'failure', 'error']) add(`status-${state}`, 'status', '', state);
for (const state of ['success', 'failure']) add(`deployment-${state}`, 'deployment_status', 'created', state);
for (const variant of ['forced', 'deleted', 'tag']) add(`push-${variant}`, 'push', '', variant);
for (const id of ['create', 'delete']) add(`${id}-tag`, id, '', 'tag');
add('code-alert-fixed', 'code_scanning_alert', 'fixed'); add('secret-alert-resolved', 'secret_scanning_alert', 'resolved');
for (const id of ['push_ruleset_bypass', 'bypass_request_secret_scanning', 'dismissal_request_dependabot', 'dismissal_request_code_scanning', 'dismissal_request_secret_scanning']) {
  for (const state of ['approved', 'rejected']) add(`${id.replaceAll('_', '-')}-${state}`, id, 'response_submitted', state);
}
add('unknown', 'klm_future_event', 'received'); fixtures.at(-1).catalogId = undefined;
for (const variant of ['missing-envelope', 'null-fields', 'unsafe-url', 'literal-unicode', 'large-number']) add(variant, 'pull_request', 'opened', variant);
add('ping', 'ping', ''); fixtures.at(-1).catalogId = undefined;

export function makePayload(fixture, options = {}) {
  const repo = options.repo ?? 'octo-fixtures/activity';
  const actor = options.actor ?? 'octo-simulator';
  const number = options.number ?? '98'; // Keep even large external IDs as strings.
  const base = options.base ?? 'main', head = options.head ?? 'feature/activity';
  const home = `https://github.com/${repo}`;
  const stamp = new Date().toISOString();
  const v = fixture.variant, id = fixture.catalogId ?? fixture.event;
  const issue = { number, title: 'Track the activity feed', body: 'A synthetic issue with useful details.', html_url: `${home}/issues/${number}`, state: fixture.action === 'closed' ? 'closed' : 'open', user: { login: actor }, labels: [{ name: 'enhancement' }] };
  const pr = { ...issue, title: 'Add retained GitHub activity', body: 'Adds a retained GitHub activity feed with event preferences and resource links.', html_url: `${home}/pull/${number}`, draft: false, changed_files: 4, merged: v === 'merged', head: { ref: head, sha: 'a'.repeat(40) }, base: { ref: base }, number };
  const comment = { id: '401', body: 'This is a synthetic comment.', html_url: `${home}/issues/${number}#issuecomment-401`, user: { login: actor }, commit_id: 'a'.repeat(40) };
  const p = { repository: { id: '1001', name: repo.split('/').at(-1), full_name: repo, html_url: home }, sender: { login: actor, id: '1002' } };
  if (fixture.action) p.action = fixture.action;
  const ci = { id: '7001', name: 'Build and type check', html_url: `${home}/actions/runs/7001`, head_branch: head, head_sha: 'a'.repeat(40), status: fixture.action === 'completed' ? 'completed' : v || 'queued', conclusion: fixture.action === 'completed' ? (v === 'null' ? null : v || 'success') : null, started_at: stamp, completed_at: fixture.action === 'completed' ? stamp : null };
  const alert = { number, html_url: `${home}/security/code-scanning/${number}`, state: 'open', rule: { description: 'Review an unsafe data flow', severity: 'warning' }, security_advisory: { summary: 'Dependency update available', severity: 'moderate' } };
  if (id.startsWith('dismissal_request_') || id === 'push_ruleset_bypass' || id === 'bypass_request_secret_scanning') {
    p.exemption_request = { number, request_type: id === 'push_ruleset_bypass' ? id : id === 'bypass_request_secret_scanning' ? 'secret_scanning_push_protection' : id.replace('dismissal_request_', '') + '_dismissal', requester_login: actor, status: fixture.action === 'created' ? 'pending' : 'completed', requester_comment: 'Synthetic request for review.', resource_identifier: `${repo}#${number}`, html_url: `${home}/security` };
    if (fixture.action === 'response_submitted') p.exemption_response = { status: v, reviewer_login: 'octo-reviewer' };
    return p;
  }
  switch (id) {
    case 'pull_request': p.number = number; p.pull_request = pr; break;
    case 'pull_request_review': p.pull_request = pr; p.review = { id: '402', state: v || 'approved', body: 'Synthetic review.', html_url: `${home}/pull/${number}#pullrequestreview-402` }; break;
    case 'pull_request_review_comment': p.pull_request = pr; p.comment = { ...comment, html_url: `${home}/pull/${number}#discussion_r401` }; break;
    case 'pull_request_review_thread': p.pull_request = pr; p.thread = { id: '403', comments: [{ ...comment, html_url: `${home}/pull/${number}#discussion_r401` }] }; break;
    case 'issues': p.issue = issue; if (fixture.action === 'assigned') p.assignee = { login: 'octo-assignee' }; break;
    case 'issue_comment': p.issue = v === 'pr' ? { ...issue, title: pr.title, html_url: pr.html_url, pull_request: { html_url: pr.html_url } } : issue; p.comment = comment; break;
    case 'issue_relates_to': p.issue = issue; if (v !== 'omitted') p.related_issue = { ...issue, number: '99', title: 'Related work' }; break;
    case 'issue_dependencies': p.blocked_issue = issue; p.blocking_issue = { ...issue, number: '99', title: 'Dependency to complete first' }; break;
    case 'sub_issues': p.sub_issue = issue; p.parent_issue = { ...issue, number: '97', title: 'Parent work' }; break;
    case 'label': p.label = { id: '501', name: 'enhancement', color: 'a2eeef' }; break;
    case 'milestone': p.milestone = { number: '3', title: 'Activity release', html_url: `${home}/milestone/3`, state: 'open' }; break;
    case 'check_run': p.check_run = { ...ci, html_url: `${home}/runs/7001` }; break;
    case 'check_suite': p.check_suite = { ...ci, app: { name: 'GitHub Actions' } }; break;
    case 'workflow_run': p.workflow_run = { ...ci, run_number: '42', display_title: 'Validate activity feed' }; p.workflow = { name: 'CI' }; break;
    case 'workflow_job': p.workflow_job = { ...ci, run_id: '7000', html_url: `${home}/actions/runs/7000/job/7001` }; break;
    case 'status': Object.assign(p, { state: v || 'success', context: 'ci/typecheck', sha: 'a'.repeat(40), target_url: `${home}/actions/runs/7001`, commit: { html_url: `${home}/commit/${'a'.repeat(40)}` } }); break;
    case 'deployment': case 'deployment_status': p.deployment = { id: '801', environment: 'staging', ref: head, sha: 'a'.repeat(40) }; if (id === 'deployment_status') p.deployment_status = { id: '802', state: v || 'success', environment_url: 'https://example.org/staging', log_url: `${home}/actions/runs/7001` }; break;
    case 'page_build': p.build = { id: '803', status: 'built', commit: 'a'.repeat(40) }; break;
    case 'push': Object.assign(p, { ref: v === 'tag' ? 'refs/tags/v0.3.0' : `refs/heads/${head}`, before: 'b'.repeat(40), after: v === 'deleted' ? '0'.repeat(40) : 'a'.repeat(40), forced: v === 'forced', deleted: v === 'deleted', created: false, compare: `${home}/compare/${base}...${head}`, head_commit: v === 'deleted' ? null : { id: 'a'.repeat(40), message: 'Connect the retained activity feed', url: `${home}/commit/${'a'.repeat(40)}` }, commits: v === 'deleted' ? [] : [{ id: 'a'.repeat(40), message: 'Connect the retained activity feed', url: `${home}/commit/${'a'.repeat(40)}` }] }); break;
    case 'create': case 'delete': p.ref = v === 'tag' ? 'v0.3.0' : head; p.ref_type = v === 'tag' ? 'tag' : 'branch'; break;
    case 'branch_protection_configuration': p.repository.default_branch = base; break;
    case 'branch_protection_rule': p.rule = { id: '901', name: base, pattern: base, required_approving_review_count: 1 }; break;
    case 'repository_ruleset': p.repository_ruleset = { id: '902', name: 'Protect main', target: 'branch', enforcement: 'active', _links: { html: { href: `${home}/rules/902` } } }; break;
    case 'merge_group': p.merge_group = { head_ref: `refs/heads/gh-readonly-queue/${base}/pr-${number}`, base_ref: `refs/heads/${base}`, head_sha: 'a'.repeat(40), base_sha: 'b'.repeat(40) }; break;
    case 'member': p.member = { login: 'octo-collaborator', id: '1003' }; break;
    case 'team_add': p.team = { id: '1004', name: 'Maintainers', slug: 'maintainers' }; break;
    case 'commit_comment': p.comment = comment; break;
    case 'deploy_key': p.key = { id: '1005', title: 'Staging read-only key', key: 'SYNTHETIC-KEY-MATERIAL-DO-NOT-DISPLAY', read_only: true }; break;
    case 'discussion': case 'discussion_comment': p.discussion = { number, title: 'Activity feed feedback', html_url: `${home}/discussions/${number}`, body: 'Synthetic discussion.', user: { login: actor } }; if (id === 'discussion_comment') p.comment = { ...comment, html_url: `${home}/discussions/${number}#discussioncomment-401` }; break;
    case 'fork': p.forkee = { id: '1006', full_name: `${actor}/activity`, html_url: `https://github.com/${actor}/activity` }; break;
    case 'meta': p.hook_id = '1007'; p.hook = { config: { secret: 'SYNTHETIC-HOOK-SECRET-DO-NOT-DISPLAY' } }; break;
    case 'package': case 'registry_package': p[id] = { id: '1008', name: 'activity-client', html_url: `https://github.com/${repo.split('/')[0]}?tab=packages`, package_version: { name: '0.3.0' } }; break;
    case 'release': p.release = { id: '1009', name: 'Activity release', tag_name: 'v0.3.0', html_url: `${home}/releases/tag/v0.3.0`, body: 'Synthetic release notes.' }; break;
    case 'repository': p.changes = { description: { from: 'Previous description' } }; break;
    case 'repository_advisory': p.repository_advisory = { ghsa_id: 'GHSA-xxxx-yyyy-zzzz', summary: 'Synthetic repository advisory', html_url: `${home}/security/advisories/GHSA-xxxx-yyyy-zzzz`, severity: 'low' }; break;
    case 'repository_import': p.status = 'success'; break;
    case 'repository_vulnerability_alert': p.alert = { id: '1010', affected_package_name: 'synthetic-package', affected_range: '< 0.3.0', external_identifier: 'CVE-SYNTHETIC', external_reference: 'https://example.org/advisory' }; break;
    case 'dependabot_alert': p.alert = { ...alert, html_url: `${home}/security/dependabot/${number}`, dependency: { package: { name: 'synthetic-package', ecosystem: 'npm' } } }; break;
    case 'code_scanning_alert': p.alert = alert; break;
    case 'secret_scanning_alert': case 'secret_scanning_alert_location': p.alert = { number, state: fixture.action === 'resolved' ? 'resolved' : 'open', html_url: `${home}/security/secret-scanning/${number}`, secret_type_display_name: 'Synthetic token', secret: 'SYNTHETIC-SECRET-DO-NOT-DISPLAY' }; if (id.endsWith('_location')) p.location = { type: 'commit', details: { path: 'example.txt', start_line: 1, end_line: 1 } }; break;
    case 'secret_scanning_scan': Object.assign(p, { type: 'backfill', source: 'repository', completed_at: stamp }); break;
    case 'security_and_analysis': p.changes = { security_and_analysis: { secret_scanning: { from: { status: 'disabled' } } } }; break;
    case 'star': p.starred_at = stamp; break;
    case 'public': p.repository.visibility = 'public'; break;
    case 'watch': break;
    case 'gollum': p.pages = [{ page_name: 'Home', title: 'Home', action: 'edited', html_url: `${home}/wiki/Home` }, { page_name: 'Activity', title: 'Activity', action: 'created', html_url: `${home}/wiki/Activity` }]; break;
    case 'ping': p.zen = 'Synthetic verification'; p.hook_id = '1007'; break;
    default: p.description = 'An unknown future event';
  }
  if (v === 'missing-envelope') { delete p.sender; delete p.repository; }
  if (v === 'null-fields') { p.pull_request = null; p.sender = null; p.repository = null; }
  if (v === 'unsafe-url') { p.pull_request.html_url = 'javascript:alert(1)'; p.repository.html_url = 'data:text/html,unsafe'; }
  if (v === 'literal-unicode') { p.pull_request.title = 'Unicode · 日本語 · ação 🚀'; p.pull_request.body = '<script>literal text only</script>\n**Not raw HTML**'; }
  if (v === 'large-number') p.pull_request.number = '18446744073709551614';
  return p;
}

export function validateFixtures() {
  const ids = new Set(families.map(([id]) => id));
  if (ids.size !== 56 || new Set(fixtures.map(f => f.name)).size !== fixtures.length) throw new Error('Invalid fixture manifest.');
  for (const id of ids) if (!fixtures.some(f => f.catalogId === id)) throw new Error(`Missing fixture: ${id}`);
  for (const f of fixtures) { const payload = JSON.parse(JSON.stringify(makePayload(f))); if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error(`Invalid fixture: ${f.name}`); }
  return { catalogIds: ids.size, scenarios: fixtures.length, confirmedHeaders: 55 };
}
