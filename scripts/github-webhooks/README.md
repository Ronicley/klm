# GitHub webhook simulator

Standalone Node 20+ CLI using native modules, with partial **synthetic** payloads.
It sends only to the supplied receiving endpoint; it never creates projects or
connections, contacts GitHub, starts agents, or retries an uncertain delivery.

```powershell
node scripts/github-webhooks/simulate.mjs --list
node scripts/github-webhooks/simulate.mjs --all --dry-run
# Configure these locally with your disposable connection's values:
$env:KLM_GITHUB_WEBHOOK_URL = 'http://127.0.0.1:27331/api/webhooks/github/CONNECTION_ID'
$env:KLM_GITHUB_WEBHOOK_SECRET = '<your disposable secret>'
node scripts/github-webhooks/simulate.mjs --fixture pr-opened
node scripts/github-webhooks/simulate.mjs --all
node scripts/github-webhooks/simulate.mjs --fixture workflow-failure --repeat 3
node scripts/github-webhooks/simulate.mjs --fixture pr-opened --repeat 2 --same-delivery
node scripts/github-webhooks/simulate.mjs --fixture pr-opened --invalid-signature
node scripts/github-webhooks/simulate.mjs --payload C:\temp\sanitized.json --event pull_request
```

`--url` and `--secret` override environment variables. Prefer the environment for
secrets. Neither secrets nor signatures/raw external payloads are printed. Requests
do not follow redirects. No scenario means help; `--all` is explicit and sequential.
`--repeat` accepts 1–1000, `--delay` 0–60000 ms. `--delivery-id` is optional;
repeating it requires `--same-delivery`. That mode freezes both header and raw
Buffer before the first request. `--all` cannot share one delivery ID.

Fixtures accept `--repo owner/name`, `--actor`, `--head`, `--base`, `--number`.
Numbers stay decimal strings. External JSON is validated as an UTF-8 object but
sent without reserialization, including original whitespace. HMAC-SHA256 signs
the exact transmitted Buffer in **X-Hub-Signature-256**. New deliveries normally
return 202, retained duplicates 200 with the same event ID. Invalid-signature mode
changes one digest nibble and expects 401; unexpected status exits nonzero.

## Coverage and limits

The manifest checks **56 catalog IDs**, with **55 confirmed canonical headers**.
Push ruleset bypass uses **klm_simulated_push_ruleset_bypass**, an explicitly
synthetic header, plus the documented
`exemption_request.request_type=push_ruleset_bypass` discriminator. This validates
structural classification, not a certified real GitHub delivery for that family.
Supply a sanitized real payload/header with `--payload --event` when available.

`--list` enumerates exact scenarios, actions, result variants, IDs and limitations.
`--all --dry-run` validates the complete manifest and generated JSON, without URL,
secret or network. All 56 families contain representative resource objects. Extra
scenarios cover PR merged/closed/review states, issue-vs-PR comments, CI null and
failure conclusions, requests approved/rejected, push deletion/tag/force, missing
fields, unsafe links, literal HTML/Unicode, large IDs, unknown types, and ping.
Ping is accepted as verification but must not appear in Activity. The synthetic
secret/key/hook-secret markers must never appear in compact feed or Details.

Fixtures do not claim complete schemas or availability on every webhook scope or
GitHub plan. Some check/merge/security actions require Apps or Enterprise. Official
sources and the unresolved header are recorded in
`GITHUB_ACTIVITY_FEED_IMPLEMENTATION_PLAN.md` sections 3 and 11. No Octokit package
or external validator is needed. Prefer entirely disposable engine data for manual
validation; this tool does not undo delivered events.
