import { RouteRequestError } from './route.js';
import { createRouteAttempt, pinHistoryIncomplete, sendRouteCommand, validRevision } from './routeCommands.js';
import { coordinate } from './routeProjection.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const validID = (value) => typeof value === 'string' && uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000';
const invalid = () => new RouteRequestError(0, 'INVALID_RESPONSE', true);
const instant = (value) => typeof value === 'string' && Number.isFinite(Date.parse(value));

export function createPanicAttempt(route, input, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (pinHistoryIncomplete(route) || !coordinate(input?.position) || !['device', 'manual'].includes(input.position_source) ||
      !['already_delayed', 'future_wait'].includes(input.delay_mode) ||
      input.delay_mode === 'already_delayed' && (!instant(input.effective_start_at) || input.delay_seconds !== undefined) ||
      input.delay_mode === 'future_wait' && (!Number.isInteger(input.delay_seconds) || input.delay_seconds < 1 || input.delay_seconds > 2147483647 || input.effective_start_at !== undefined)) throw new RouteRequestError(0, 'INVALID_PANIC', false);
  const body = { route_id: base.routeID, delay_mode: input.delay_mode, position: { latitude: input.position.latitude, longitude: input.position.longitude }, position_source: input.position_source };
  if (input.delay_mode === 'already_delayed') body.effective_start_at = input.effective_start_at;
  else body.delay_seconds = input.delay_seconds;
  return Object.freeze({ ...base, operation: 'panic', path: '/api/v1/routes/reroute/panic', body: JSON.stringify(body), basis: JSON.stringify(route.plan) });
}

export function createRemovalAttempt(route, visitID, mode, acknowledge, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  const step = route.plan.steps.find((item) => item.visit_id === visitID);
  if (!validID(visitID) || !step || !['rebuild', 'free_time'].includes(mode) || typeof acknowledge !== 'boolean' || pinHistoryIncomplete(route) ||
      route.execution?.some((item) => item.visit_id === visitID && ['completed', 'skipped'].includes(item.status))) {
    throw new RouteRequestError(0, 'INVALID_REMOVAL', false);
  }
  if (!acknowledge && ['user_reported_confirmed', 'provider_confirmed'].includes(step.participation.status)) {
    throw new RouteRequestError(0, 'EXTERNAL_COMMITMENT_CONFIRMATION_REQUIRED', false);
  }
  return Object.freeze({ ...base, operation: 'removal', visitID, mode,
    path: `/api/v1/routes/${base.routeID}/visits/${visitID.toLowerCase()}/removal-proposal`,
    body: JSON.stringify({ mode, acknowledge_external_commitment: acknowledge }),
    basis: JSON.stringify(route.plan),
  });
}

export function createProposalResolutionAttempt(operation, route, proposal, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (!['apply', 'reject'].includes(operation) || !validID(proposal?.proposal_id) || proposal.state !== 'pending' ||
      !validRevision(proposal.base_revision) || proposal.base_revision !== route.revision) {
    throw new RouteRequestError(0, 'INVALID_PROPOSAL', false);
  }
  const id = proposal.proposal_id.toLowerCase();
  return Object.freeze({ ...base, operation, proposalID: id,
    path: operation === 'apply' ? `/api/v1/routes/${base.routeID}/apply` : `/api/v1/routes/${base.routeID}/proposals/${id}/reject`,
    body: operation === 'apply' ? JSON.stringify({ proposal_id: id }) : undefined,
  });
}

function validConflicts(conflicts) {
  return Array.isArray(conflicts) && conflicts.every((item) => typeof item?.code === 'string' && item.code &&
    typeof item.message === 'string' && item.message.trim() && Array.isArray(item.visit_ids) && item.visit_ids.every(validID) &&
    (item.session_ids === undefined || Array.isArray(item.session_ids) && item.session_ids.every(validID)));
}

function validChanges(changes, beforeIDs, afterIDs) {
  return Array.isArray(changes) && changes.every((item) => {
    if (!item || !['kept', 'removed', 'replaced', 'time_shifted', 'cost_changed', 'participation_action', 'verification_changed'].includes(item.kind) ||
        !['route', 'visit', 'leg'].includes(item.scope) || typeof item.message !== 'string' || !item.message.trim() || [...item.message].length > 512 ||
        item.before_visit_id !== undefined && (!validID(item.before_visit_id) || beforeIDs && !beforeIDs.has(item.before_visit_id)) ||
        item.after_visit_id !== undefined && (!validID(item.after_visit_id) || afterIDs && !afterIDs.has(item.after_visit_id))) return false;
    if (item.scope === 'visit' && ((!item.before_visit_id && !item.after_visit_id) || item.leg_position !== undefined) ||
        item.scope === 'leg' && (!Number.isInteger(item.leg_position) || item.leg_position < 1 || item.before_visit_id || item.after_visit_id) ||
        item.scope === 'route' && (item.before_visit_id || item.after_visit_id || item.leg_position !== undefined)) return false;
    if (item.kind === 'removed' && (!item.before_visit_id || item.after_visit_id !== undefined) ||
        item.kind === 'replaced' && (!item.before_visit_id || !item.after_visit_id) ||
        item.kind === 'time_shifted' && (!Number.isInteger(item.time_shift_seconds) || item.time_shift_seconds < -2147483648 || item.time_shift_seconds > 2147483647) ||
        item.kind === 'participation_action' && (typeof item.action !== 'string' || !item.action.trim() || [...item.action].length > 128)) return false;
    return true;
  });
}

function validateProposal(proposal, attempt, changes) {
  const base = JSON.parse(attempt.basis);
  const plan = proposal?.candidate;
  if (!validID(proposal?.proposal_id) || proposal.state !== 'pending' || proposal.reason !== (attempt.operation === 'panic' ? 'delay' : 'delete') ||
      proposal.base_revision !== attempt.revision || !validRevision(proposal.base_catalog_revision) || !instant(proposal.created_at) ||
      !plan || !['READY', 'PARTIAL'].includes(plan.result) || !Array.isArray(plan.steps) || !Array.isArray(plan.legs) ||
      plan.catalog_revision !== proposal.base_catalog_revision || !validRevision(base.catalog_revision) || BigInt(plan.catalog_revision) < BigInt(base.catalog_revision) ||
      plan.archetype_id !== base.archetype_id || plan.lifecycle !== base.lifecycle || plan.timezone !== base.timezone ||
      !instant(plan.start_at) || !instant(plan.end_at) || Date.parse(plan.start_at) !== Date.parse(base.start_at) || Date.parse(plan.end_at) !== Date.parse(base.end_at) ||
      !coordinate(plan.origin) || plan.origin.latitude !== base.origin.latitude || plan.origin.longitude !== base.origin.longitude ||
      Boolean(plan.destination) !== Boolean(base.destination) || plan.destination && (!coordinate(plan.destination) || plan.destination.latitude !== base.destination.latitude || plan.destination.longitude !== base.destination.longitude) ||
      !validConflicts(proposal.conflicts) || proposal.conflicts.length) throw invalid();
  const beforeIDs = new Set(base.steps.map((step) => step.visit_id));
  if (attempt.operation === 'panic') {
    const input = JSON.parse(attempt.body);
    if (!instant(proposal.effective_start_at) || input.delay_mode === 'already_delayed' && Date.parse(proposal.effective_start_at) !== Date.parse(input.effective_start_at)) throw invalid();
  }
  const afterIDs = new Set(plan.steps.map((step) => step.visit_id));
  if (afterIDs.size !== plan.steps.length || attempt.operation === 'removal' && afterIDs.has(attempt.visitID) ||
      plan.steps.some((step, index) => !validID(step.visit_id) || !['visit', 'free_time'].includes(step.kind) || step.position !== index + 1 ||
        !instant(step.arrival_at) || !instant(step.visit_start_at) || !instant(step.visit_end_at) || !instant(step.departure_at) ||
        Date.parse(step.arrival_at) > Date.parse(step.visit_start_at) || Date.parse(step.visit_start_at) >= Date.parse(step.visit_end_at) || Date.parse(step.visit_end_at) > Date.parse(step.departure_at)) ||
      !validChanges(proposal.changes, beforeIDs, afterIDs) || !validChanges(changes, beforeIDs, afterIDs) || JSON.stringify(proposal.changes) !== JSON.stringify(changes)) throw invalid();
  const selected = base.steps.find((step) => step.visit_id === attempt.visitID);
  if (attempt.operation === 'panic') return;
  const removal = changes.filter((item) => item.before_visit_id === attempt.visitID && ['removed', 'replaced'].includes(item.kind));
  if (removal.length !== 1 || attempt.mode === 'rebuild' && removal[0].kind !== 'removed') throw invalid();
  if (attempt.mode === 'free_time') {
    const pause = plan.steps.find((step) => step.visit_id === removal[0].after_visit_id);
    if (removal[0].kind !== 'replaced' || !pause || pause.kind !== 'free_time' || beforeIDs.has(pause.visit_id) ||
        Date.parse(pause.visit_start_at) !== Date.parse(selected.visit_start_at) || Date.parse(pause.visit_end_at) !== Date.parse(selected.visit_end_at)) throw invalid();
  }
}

export async function sendProposalCommand(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  if (attempt.operation === 'apply') {
    const result = await sendRouteCommand(apiBaseUrl, accessToken, attempt, fetcher, signal);
    if (!['READY', 'PARTIAL'].includes(result.status) || BigInt(result.revision) !== BigInt(attempt.revision) + 1n) throw invalid();
    return result;
  }
  if (!accessToken) throw new RouteRequestError(401, 'AUTH_REQUIRED', false);
  if (!['removal', 'panic', 'reject'].includes(attempt.operation)) throw invalid();
  const response = await fetcher(`${apiBaseUrl}${attempt.path}`, {
    method: 'POST', headers: { Authorization: `Bearer ${accessToken}`, 'Idempotency-Key': attempt.key, 'If-Match': `"${attempt.revision}"`,
      ...(attempt.body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: attempt.body, credentials: 'omit', cache: 'no-store', signal,
  });
  let body;
  try { body = await response.json(); } catch { throw invalid(); }
  if (!response.ok) {
    if (typeof body?.code !== 'string' || typeof body.retryable !== 'boolean') throw invalid();
    throw new RouteRequestError(response.status, body.code, body.retryable);
  }
  if (response.status !== 200 || typeof body?.request_id !== 'string' || !body.request_id || typeof body.route_id !== 'string' || body.route_id.toLowerCase() !== attempt.routeID ||
      body.revision !== attempt.revision || !validConflicts(body.conflicts) || !validChanges(body.changes) ||
      !['PROPOSED', 'CONFLICT', 'UNCHANGED'].includes(body.status) || body.status === 'CONFLICT' && !body.conflicts.length || body.status !== 'CONFLICT' && body.conflicts.length ||
      attempt.operation === 'reject' && (body.status !== 'UNCHANGED' || body.changes.length)) throw invalid();
  if (body.status === 'PROPOSED') validateProposal(body.proposal, attempt, body.changes);
  else if (body.proposal !== undefined || body.status === 'UNCHANGED' && body.changes.length) throw invalid();
  return body;
}
