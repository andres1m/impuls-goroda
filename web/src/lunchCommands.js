import { RouteRequestError } from './route.js';
import { createRouteAttempt, validRevision } from './routeCommands.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const invalid = () => new RouteRequestError(0, 'INVALID_LUNCH', false);
const validID = (id) => typeof id === 'string' && uuid.test(id);

export function venueForLunch(existing, selected, freeTime) {
  if (freeTime) return undefined;
  if (selected) return { provider: '2gis', external_id: selected.cafe.external_id };
  if (existing?.external_venue) return { provider: '2gis', external_id: existing.external_venue.external_id };
  if (existing?.kind === 'visit' && existing.catalog) return { catalog_visit_id: existing.visit_id };
  return undefined;
}

export function createLunchAttempt(route, input, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (route.lifecycle !== 'saved' || route.pending_proposal || !['add', 'update', 'remove'].includes(input?.action)) throw invalid();
  const existing = route.plan?.steps?.find((step) => step.visit_id === input.lunch_id && Boolean(step.lunch));
  if (input.action !== 'add' && (!validID(input.lunch_id) || !existing || typeof input.acknowledge_external_commitment !== 'boolean')) throw invalid();
  if (input.action !== 'remove') {
    const placement = input.placement;
    if (!validID(placement?.after_visit_id) || !route.plan.steps.some((step) => step.visit_id === placement.after_visit_id && step.kind === 'visit') ||
      ![2700, 3600].includes(placement.duration_seconds) || input.venue &&
      !(input.venue.provider === '2gis' && typeof input.venue.external_id === 'string' && input.venue.external_id.length > 0 && input.venue.external_id.length <= 128 || validID(input.venue.catalog_visit_id))) throw invalid();
  }
  const body = input.action === 'add' ? { action: 'add', placement: { ...input.placement }, ...(input.venue ? { venue: { ...input.venue } } : {}) }
    : input.action === 'remove' ? { action: 'remove', lunch_id: input.lunch_id, acknowledge_external_commitment: input.acknowledge_external_commitment }
      : { action: 'update', lunch_id: input.lunch_id, placement: { ...input.placement }, ...(input.venue ? { venue: { ...input.venue } } : {}), acknowledge_external_commitment: input.acknowledge_external_commitment };
  return Object.freeze({ ...base, operation: 'lunch', path: `/api/v1/routes/${base.routeID}/lunch/proposals`, body: JSON.stringify(body) });
}

export async function sendLunchCommand(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  if (!accessToken) throw new RouteRequestError(401, 'AUTH_REQUIRED', false);
  const response = await fetcher(`${apiBaseUrl}${attempt.path}`, { method: 'POST', headers: {
    Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json', 'Idempotency-Key': attempt.key, 'If-Match': `"${attempt.revision}"`,
  }, credentials: 'omit', cache: 'no-store', body: attempt.body, signal });
  let body;
  try { body = await response.json(); } catch { throw new RouteRequestError(0, 'INVALID_RESPONSE', true); }
  if (!response.ok) {
    if (typeof body?.code !== 'string' || typeof body.retryable !== 'boolean') throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
    throw new RouteRequestError(response.status, body.code, body.retryable);
  }
  if (response.status !== 200 || body?.route_id?.toLowerCase() !== attempt.routeID || body.revision !== attempt.revision ||
    typeof body.request_id !== 'string' || !body.request_id || !['PROPOSED', 'UNCHANGED', 'CONFLICT'].includes(body.status) ||
    !Array.isArray(body.changes) || !Array.isArray(body.conflicts) ||
    body.status === 'PROPOSED' && (!validID(body.proposal?.proposal_id) || body.proposal.state !== 'pending' || body.proposal.reason !== 'lunch' ||
      body.proposal.base_revision !== attempt.revision || !validRevision(body.proposal.base_catalog_revision) || !Array.isArray(body.proposal.candidate?.steps) || body.conflicts.length) ||
    body.status !== 'PROPOSED' && body.proposal !== undefined || body.status === 'CONFLICT' && !body.conflicts.length ||
    body.status === 'UNCHANGED' && (body.conflicts.length || body.changes.length)) throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
  return body;
}
