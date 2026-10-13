#!/usr/bin/env node
import { createHmac, randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { fixtures, makePayload, validateFixtures } from './fixtures.mjs';

const help = `GitHub webhook simulator (partial synthetic fixtures)
  --list                         Show scenarios and coverage
  --all --dry-run                 Validate all fixtures without network
  --fixture pr-opened             Send one scenario
  --payload path.json --event pull_request  Send original external bytes
  --url URL --secret SECRET       Or KLM_GITHUB_WEBHOOK_URL / KLM_GITHUB_WEBHOOK_SECRET
  --repo owner/name --actor LOGIN --head REF --base REF --number ID
  --repeat N --delay MS --delivery-id ID --same-delivery --invalid-signature
No scenario means help only. No project/connection setup or automatic retries.`;

async function main() {
  const flags = new Set(['help', 'list', 'all', 'dry-run', 'same-delivery', 'invalid-signature']);
  const values = new Set(['fixture', 'payload', 'event', 'url', 'secret', 'repo', 'actor', 'head', 'base', 'number', 'repeat', 'delay', 'delivery-id']);
  const options = Object.create(null), args = process.argv.slice(2);
  for (let i = 0; i < args.length; i++) {
    const key = args[i].startsWith('--') ? args[i].slice(2) : '';
    if (flags.has(key)) options[key] = true;
    else if (values.has(key) && args[i + 1] !== undefined && !args[i + 1].startsWith('--')) options[key] = args[++i];
    else throw new Error('Unknown option or missing option value. Use --help.');
  }
  const coverage = validateFixtures();
  if (options.help || (!options.list && !options.all && !options.fixture && !options.payload)) { console.log(help); return; }
  if (options.list) {
    console.log(`Coverage: ${coverage.catalogIds} catalog IDs; ${coverage.confirmedHeaders} confirmed headers; ${coverage.scenarios} scenarios.`);
    for (const f of fixtures) console.log(`${f.name}\t${f.event}\t${f.catalogId ?? '(control/unknown)'}\t${f.action || '—'} ${f.variant}\t${f.limitation}`);
    return;
  }
  if ([options.all, options.fixture, options.payload].filter(Boolean).length !== 1) throw new Error('Choose exactly one of --all, --fixture, or --payload.');
  if (options.all && (options['same-delivery'] || options['delivery-id'])) throw new Error('--all cannot share a delivery ID across distinct scenarios.');
  if (options['delivery-id'] && Number(options.repeat ?? 1) > 1 && !options['same-delivery']) throw new Error('Use --same-delivery to repeat an explicit delivery ID.');
  const repeat = Number(options.repeat ?? 1), delay = Number(options.delay ?? 0);
  if (!Number.isInteger(repeat) || repeat < 1 || repeat > 1000 || !Number.isInteger(delay) || delay < 0 || delay > 60000) throw new Error('Repeat must be 1–1000 and delay 0–60000 ms.');
  if (options.repo && !/^[\w.-]+\/[\w.-]+$/.test(options.repo)) throw new Error('--repo must be owner/name.');
  if (options.number && !/^[0-9]+$/.test(options.number)) throw new Error('--number must be a decimal string.');
  let selected;
  if (options.payload) {
    if (!options.event) throw new Error('--payload requires --event.');
    let raw;
    try { raw = await readFile(options.payload); } catch { throw new Error('Could not read the external payload file.'); }
    try {
      const decoded = new TextDecoder('utf-8', { fatal: true }).decode(raw), payload = JSON.parse(decoded);
      if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error();
    } catch { throw new Error('External payload must be a UTF-8 JSON object.'); }
    selected = [{ name: 'external-payload', event: options.event, raw, limitation: 'external original bytes; schema not certified' }];
  } else {
    selected = options.all ? fixtures : fixtures.filter(f => f.name === options.fixture);
    if (!selected.length) throw new Error('Unknown fixture. Use --list.');
    selected = selected.map(f => ({ ...f, raw: Buffer.from(JSON.stringify(makePayload(f, options)), 'utf8') }));
    if (options.event) { if (options.all) throw new Error('--event cannot override --all.'); selected[0].event = options.event; }
  }
  for (const f of selected) {
    if (!/^[!#$%&'*+.^_`|~0-9a-z-]{1,100}$/i.test(f.event) || f.raw.length > 25 * 1024 * 1024) throw new Error('Invalid event header or payload exceeds 25 MiB.');
  }
  if (options['delivery-id'] && !/^[\x21-\x7e]{1,128}$/.test(options['delivery-id'])) throw new Error('Invalid delivery ID.');
  if (options['dry-run']) {
    console.log(`Dry run: ${coverage.catalogIds} catalog IDs / ${coverage.scenarios} scenarios available; no network.`);
    for (const f of selected) console.log(`${f.name}: event=${f.event}, catalog=${f.catalogId ?? 'none'}, bytes=${f.raw.length}, repeat=${repeat}; ${f.limitation}`);
    return;
  }
  let url;
  try { url = new URL(options.url ?? process.env.KLM_GITHUB_WEBHOOK_URL); } catch { throw new Error('Provide a valid --url or KLM_GITHUB_WEBHOOK_URL.'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new Error('Endpoint must be HTTP(S), without credentials or fragment.');
  const secret = options.secret ?? process.env.KLM_GITHUB_WEBHOOK_SECRET;
  if (!secret) throw new Error('Provide --secret or KLM_GITHUB_WEBHOOK_SECRET.');
  for (const f of selected) {
    // Freeze the raw buffer and header before repeats: same-delivery is byte exact.
    const sharedDelivery = options['delivery-id'] ?? randomUUID();
    let signature = createHmac('sha256', secret).update(f.raw).digest('hex');
    if (options['invalid-signature']) signature = (signature[0] === '0' ? '1' : '0') + signature.slice(1);
    for (let i = 0; i < repeat; i++) {
      const delivery = options['same-delivery'] || options['delivery-id'] ? sharedDelivery : randomUUID();
      let response;
      try { response = await fetch(url, { method: 'POST', redirect: 'manual', signal: AbortSignal.timeout(30000), headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': f.event, 'X-GitHub-Delivery': delivery, 'X-Hub-Signature-256': `sha256=${signature}` }, body: f.raw }); }
      catch { throw new Error('Webhook request failed or timed out. Delivery may be uncertain; no retry was sent.'); }
      const result = await response.json().catch(() => null);
      const eventId = typeof result?.eventId === 'string' && /^[a-f0-9]{32}$/.test(result.eventId) ? result.eventId : undefined;
      console.log(JSON.stringify({ fixture: f.name, occurrence: i + 1, status: response.status, accepted: typeof result?.accepted === 'boolean' ? result.accepted : undefined, duplicate: typeof result?.duplicate === 'boolean' ? result.duplicate : undefined, eventId }));
      const expected = options['invalid-signature'] ? response.status === 401 : response.status === 202 || response.status === 200 && result?.duplicate === true;
      if (!expected) throw new Error(`Unexpected HTTP status ${response.status}; stopped without retry.`);
      if (delay) await new Promise(resolve => setTimeout(resolve, delay));
    }
  }
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
