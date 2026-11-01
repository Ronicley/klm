// Versioned engine-owned wrapper protocol. Materialized only by a released adapter.
export const delegationMetadata = /*KLM_DELEGATION_METADATA*/{};
const parents = new Set(['', 'side_agent', 'general_agent', 'graph_node']);
const outcomes = new Set(['completed', 'failed', 'cancelled', 'interrupted', 'finality_unconfirmed']);
const maxPayload = 2 * 1024 * 1024;
const bytes = value => new TextEncoder().encode(value).length;
const text = (value, max) => typeof value === 'string' && value.trim() !== '' && bytes(value) <= max;
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const canonical = value => JSON.stringify(value, (_key, item) => object(item) ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item);
export const delegationParentEligible = role => parents.has(role);
// Defense in depth only: the child's separately authenticated role catalog is
// external-only. Never derive a complete child capability set from parent names.
export const delegationLeafForbidden = name => /^(task|subagent|subagent_run|feed_read)$/.test(name) || /^(klm_linked_|linked_|sessions?_|graphs?_|projects?_|automations?_)/.test(name);

export function validateDelegationRequest(request) {
  if (!object(request) || Object.keys(request).some(key => !['operationId', 'children'].includes(key)) || !text(request.operationId, 200) || !Array.isArray(request.children) || request.children.length < 1 || request.children.length > 8 || bytes(JSON.stringify(request)) > maxPayload) throw new Error('Supply operationId and one to eight children in one batch.');
  const keys = new Set(), children = new Set();
  for (const child of request.children) {
    const allowed = ['key', 'task', 'title', 'childSessionId', 'agentId', 'harness', 'model', 'effort', 'inheritParentCapabilities'];
    if (!object(child) || Object.keys(child).some(key => !allowed.includes(key)) || !text(child.key, 80) || keys.has(child.key) || !text(child.task, 128 * 1024)) throw new Error('Invalid child key/task or unknown delegation field.');
    keys.add(child.key);
    if ('title' in child && (typeof child.title !== 'string' || child.title.trim() === '' || [...child.title].length > 200)) throw new Error('Invalid child title.');
    for (const name of ['childSessionId', 'agentId']) if (name in child && !text(child[name], name === 'agentId' ? 200 : 256)) throw new Error('Invalid exact child/agent identity.');
    if ('harness' in child && !['pi', 'opencode', 'codex'].includes(child.harness)) throw new Error('Invalid child harness.');
    for (const name of ['model', 'effort']) if (name in child && (typeof child[name] !== 'string' || bytes(child[name]) > 256)) throw new Error('Invalid explicit model/effort.');
    if ('inheritParentCapabilities' in child && typeof child.inheritParentCapabilities !== 'boolean') throw new Error('External inheritance must be a creation-only boolean.');
    if (child.childSessionId) {
      if (children.has(child.childSessionId) || child.inheritParentCapabilities === true) throw new Error('Child reuse must be unique and cannot recapture parent capabilities.');
      children.add(child.childSessionId);
    }
  }
}

function validateResult(result, request, batchId) {
  if (!object(result) || result.version !== 1 || result.batchId !== batchId || result.operationId !== request.operationId || !outcomes.has(result.outcome) || typeof result.isError !== 'boolean' || result.canContinue !== true || !Array.isArray(result.children) || result.children.length !== request.children.length) throw new Error('Missing, partial or unsafe consolidated delegation result.');
  const ids = new Set();
  for (const [index, child] of result.children.entries()) {
    const f = child.finality;
    if (child.key !== request.children[index].key || !text(child.invocationId, 256) || !text(child.childSessionId, 256) || ids.has(child.childSessionId) || !outcomes.has(child.outcome) || child.outcome === 'finality_unconfirmed' || !f || f.nativeSettled !== true || f.processesDrained !== true || f.toolCallsSettled !== true || (f.uncertainToolCalls?.length ?? 0) !== 0) throw new Error('Child result has unconfirmed identity or drainage.');
    if ((child.outcome !== 'completed' || child.taskOutcome === 'unsatisfied') && !result.isError) throw new Error('Failed child cannot be a successful tool result.');
    ids.add(child.childSessionId);
  }
  if (bytes(JSON.stringify(result)) > 32 * 1024) throw new Error('Consolidated result exceeds the native output budget.');
}

export function createDelegationTransport(config) {
  let ready = false, active, fatalStop;
  const fatalSignal = new AbortController();
  if (!delegationParentEligible(config.parentRole)) throw new Error('Leaf roles cannot own delegation transport.');
  const endpoint = new URL(config.bridgeUrl);
  if (endpoint.protocol !== 'http:' || !['127.0.0.1', '[::1]'].includes(endpoint.hostname) || !endpoint.port || endpoint.username || endpoint.password || endpoint.search || endpoint.hash || !text(config.bridgeToken, 4096)) throw new Error('Delegation requires the owned literal-loopback capability.');
  async function rpc(method, params, signal, budget = 10000) {
    const signals = [AbortSignal.timeout(budget)];
    if (signal) signals.push(signal);
    if (method !== 'klm/delegation/cancel') signals.push(fatalSignal.signal);
    const response = await fetch(endpoint, {
      method: 'POST', headers: { 'content-type': 'application/json', authorization: 'Bearer ' + config.bridgeToken },
      body: JSON.stringify({ jsonrpc: '2.0', id: crypto.randomUUID(), method, params }),
      signal: AbortSignal.any(signals),
    });
    if (!response.ok) throw new Error('Owned delegation transport is unavailable.');
    const raw = await response.text();
    if (bytes(raw) > maxPayload + 8192) throw new Error('Delegation reply exceeds transport limit.');
    const envelope = JSON.parse(raw);
    if (envelope.error || !object(envelope.result)) {
      const error = new Error(envelope.error?.message ?? 'Invalid delegation RPC reply.');
      const rejection = envelope.error?.data;
      const provenAdmissionRejection = method === 'klm/delegation/admit' || method === 'klm/delegation/prepared' && rejection?.phase === 'pre_admission' && rejection.preparationId === params.preparationId;
      if (provenAdmissionRejection && rejection?.accepted === false && ['invalid_request', 'batch_busy', 'operation_conflict', 'child_busy', 'configuration_blocked'].includes(rejection.code)) error.delegationInputRejected = true;
      throw error;
    }
    return envelope.result;
  }
  function assertNotStopped() {
    if (fatalStop) throw fatalStop;
  }
  function requireOwnerStop(cause) {
    if (!fatalStop) {
      // Do not carry a pre-admission marker into a post-admission failure.
      fatalStop = new Error('Owning execution must stop before continuing: ' + (cause?.message ?? 'delegation finality is uncertain'));
      fatalStop.delegationStopRequired = true;
      fatalSignal.abort(fatalStop);
    }
    return fatalStop;
  }
  async function cancelBestEffort(execution) {
    if (!execution.batchId) return;
    if (!execution.cancelPromise) execution.cancelPromise = rpc('klm/delegation/cancel', { batchId: execution.batchId }, undefined, 1000).catch(() => {});
    await execution.cancelPromise;
  }
  async function ensureReady(signal) {
    assertNotStopped();
    if (ready) return;
    try {
      const response = await rpc('klm/delegation/ready', { version: 1, specDigest: delegationMetadata.specDigest }, signal);
      assertNotStopped();
      if (response.version !== 1 || response.specDigest !== delegationMetadata.specDigest || response.maxChildren !== 8 || response.maxConcurrency !== 4 || response.activeBatchLimit !== 1) throw new Error('Delegation runtime/schema handshake failed.');
      ready = true;
    } catch (error) { throw requireOwnerStop(error); }
  }
  async function admissionReady(request, nativeCallId, signal) {
    let response = await rpc('klm/delegation/admit', { version: 1, nativeCallId, specDigest: delegationMetadata.specDigest, request }, signal);
    if (response.preparing === true) {
      if (response.version !== 1 || !text(response.preparationId, 256) || response.receipt || response.accepted) throw new Error('Invalid owner-bound preparation identity.');
      const preparationId = response.preparationId;
      do {
        assertNotStopped();
        // Discovery/capture can outlive HTTP. Continue a KNOWN preparation with
        // bounded reads; never resend an admission whose response was uncertain.
        response = await rpc('klm/delegation/prepared', { preparationId, nativeCallId, waitMs: 8000 }, signal);
        assertNotStopped();
        if (response.version !== 1 || response.preparationId !== preparationId) throw new Error('Preparation continuation changed identity.');
        if (response.preparing === true && (response.receipt || response.accepted)) throw new Error('Preparation mixed waiting and acceptance.');
      } while (response.preparing === true && !response.receipt);
    }
    return response.receipt ?? response;
  }
  async function execute(request, nativeCallId, signal) {
    assertNotStopped();
    try { validateDelegationRequest(request); }
    catch (error) { error.delegationInputRejected = true; throw error; }
    if (!signal || signal.aborted || !text(nativeCallId, 512)) throw requireOwnerStop(new Error('Inactive/unidentified native delegation call.'));
    const fingerprint = canonical(request);
    if (active) {
      if (active.fingerprint !== fingerprint) { const error = new Error('batch_busy: one outstanding batch per parent.'); error.delegationInputRejected = true; throw error; }
      const existing = active;
      existing.users++;
      let accepted = false;
      try {
        await ensureReady(signal);
        // Persist this alias on the SAME operation. An uncertain alias reply is
        // fatal even if the original call later returns a valid full result.
        const alias = await admissionReady(request, nativeCallId, signal);
        if (text(alias.batchId, 256) && !existing.batchId) existing.batchId = alias.batchId;
        accepted = true;
        assertNotStopped();
        if (alias.accepted !== true || alias.version !== 1 || alias.operationId !== request.operationId || alias.childCount !== request.children.length || alias.maxConcurrency !== 4) throw new Error('Delegation retry receipt changed.');
        await existing.promise;
		// Every native alias obtains its own sending transition and result receipt.
		const response = await rpc('klm/delegation/wait', { batchId: alias.batchId, nativeCallId, revision: '', waitMs: 8000 }, signal);
		const result = response.result;
		if (response.state !== 'settled' || response.stopRequired) throw new Error('Alias result is not settled.');
		validateResult(result, request, alias.batchId);
        assertNotStopped();
        if (alias.batchId !== result.batchId) throw new Error('Delegation retry receipt changed.');
        return result;
      } catch (error) {
        if (!fatalStop && !accepted && error.delegationInputRejected) throw error;
        const failure = requireOwnerStop(error);
        await cancelBestEffort(existing);
        throw failure;
      } finally {
        if (--existing.users === 0 && active === existing) active = undefined;
      }
    }
    const execution = { fingerprint, users: 1 };
    active = execution;
    const operation = (async () => {
      await ensureReady(signal);
      const receipt = await admissionReady(request, nativeCallId, signal);
      if (text(receipt.batchId, 256)) execution.batchId = receipt.batchId;
      assertNotStopped();
      if (receipt.accepted !== true || receipt.version !== 1 || !text(receipt.batchId, 256) || receipt.operationId !== request.operationId || receipt.childCount !== request.children.length || receipt.maxConcurrency !== 4) throw new Error('Invalid delegation admission receipt.');
      let revision = '';
      while (!signal.aborted) {
        // Each infrastructure wait is <=8s at the server, <=10s here. The native
        // tool remains pending: no admission receipt/partial result reaches LLM.
        const response = await rpc('klm/delegation/wait', { batchId: receipt.batchId, nativeCallId, revision, waitMs: 8000 }, signal);
        assertNotStopped();
        if (typeof response.revision !== 'string') throw new Error('Invalid delegation revision.');
        revision = response.revision;
        if (response.state === 'waiting' && !response.result && !response.stopRequired) continue;
        if (response.state !== 'settled' || response.stopRequired) throw new Error('Owning runtime must stop the parent; no safe native result exists.');
        validateResult(response.result, request, receipt.batchId);
        assertNotStopped();
        return response.result;
      }
      signal.throwIfAborted();
    })();
    execution.promise = operation;
    try { const result = await operation; assertNotStopped(); return result; }
    catch (error) {
      if (!fatalStop && !execution.batchId && error.delegationInputRejected) throw error;
      const failure = requireOwnerStop(error); // Arm synchronously, BEFORE cancel/abort awaits.
      await cancelBestEffort(execution);
      throw failure;
    } finally {
      if (--execution.users === 0 && active === execution) active = undefined;
      // Clearing a settled Promise never clears fatalStop. Only disposal of this
      // execution's transport by the owning runtime ends the local stop barrier.
    }
  }
  async function gate(tool, signal) {
    assertNotStopped();
    await ensureReady(signal);
    assertNotStopped();
    if (tool === 'task' || tool === 'subagent') throw new Error('Native delegation is disabled by the owned replacement.');
    if (active && tool !== delegationMetadata.name) throw new Error('Parent is waiting for its admitted children.');
    try {
      const admission = await rpc('klm/delegation/gate', { tool }, signal);
      assertNotStopped();
      if (admission.allowed !== true) throw new Error('Owned runtime refused parent tool admission.');
    } catch (error) { throw requireOwnerStop(error); }
  }
  function assertCanContinue() {
    assertNotStopped();
    if (active) throw new Error('Parent model iteration attempted before delegation settlement.');
  }
  return { ensureReady, execute, gate, assertCanContinue, canContinue: () => !active && !fatalStop, isStopRequired: () => !!fatalStop, isWaiting: () => !!active };
}
