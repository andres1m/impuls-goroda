import { RouteRequestError } from './route.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const validRevision = (value) => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;

export function createRouteAttempt(operation, route, key = crypto.randomUUID()) {
  if (!['selection', 'save'].includes(operation) || !uuid.test(route?.route_id) || !validRevision(route.revision) || !uuid.test(key)) {
    throw new RouteRequestError(0, 'INVALID_RESPONSE', false);
  }
  const id = route.route_id.toLowerCase();
  return Object.freeze({ operation, routeID: id, revision: route.revision, key,
    path: operation === 'selection' ? '/api/v1/me/context/selection' : `/api/v1/routes/${id}/save`,
    body: operation === 'selection' ? JSON.stringify({ route_id: id }) : undefined,
  });
}

export function createExecutionAttempt(route, visitID, status, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (!uuid.test(visitID) || !['completed', 'skipped'].includes(status) || !route.plan.steps.some((step) => step.visit_id === visitID && step.kind === 'visit')) {
    throw new RouteRequestError(0, 'INVALID_EXECUTION', false);
  }
  const current = route.execution?.find((item) => item.visit_id === visitID);
  if (current?.confirmation_kind === 'provider_confirmed') throw new RouteRequestError(0, 'PROVIDER_CONFIRMED', false);
  const input = { status, confirmation_kind: 'user_reported' };
  if (current?.actual_started_at) input.actual_started_at = current.actual_started_at;
  if (current?.actual_ended_at) input.actual_ended_at = current.actual_ended_at;
  return Object.freeze({ ...base, operation: 'execution', visitID, executionStatus: status,
    path: `/api/v1/routes/${base.routeID}/visits/${visitID.toLowerCase()}/execution`, body: JSON.stringify(input),
  });
}

export function createParticipationAttempt(route, visitID, action, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (!uuid.test(visitID) || !['user_reported_confirmed', 'user_reported_unavailable', 'clear_user_report'].includes(action) || !route.plan.steps.some((step) => step.visit_id === visitID && step.kind === 'visit')) {
    throw new RouteRequestError(0, 'INVALID_PARTICIPATION', false);
  }
  const current = route.participation?.find((item) => item.visit_id === visitID);
  if (current?.evidence === 'provider') throw new RouteRequestError(0, 'PROVIDER_CONFIRMED', false);
  const input = { action };
  if (action === 'user_reported_confirmed' && current?.private_reference) input.private_reference = current.private_reference;
  return Object.freeze({ ...base, operation: 'participation', visitID,
    path: `/api/v1/routes/${base.routeID}/visits/${visitID.toLowerCase()}/participation`, body: JSON.stringify(input),
  });
}

export async function sendRouteCommand(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  if (!accessToken) throw new RouteRequestError(401, 'AUTH_REQUIRED', false);
  const response = await fetcher(`${apiBaseUrl}${attempt.path}`, {
    method: 'POST', headers: { Authorization: `Bearer ${accessToken}`, 'Idempotency-Key': attempt.key,
      'If-Match': `"${attempt.revision}"`, ...(attempt.body === undefined ? {} : { 'Content-Type': 'application/json' }),
    }, body: attempt.body, credentials: 'omit', cache: 'no-store', signal,
  });
  let body;
  try { body = await response.json(); } catch { throw new RouteRequestError(0, 'INVALID_RESPONSE', true); }
  if (!response.ok) {
    if (typeof body?.code !== 'string' || typeof body.retryable !== 'boolean') throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
    throw new RouteRequestError(response.status, body.code, body.retryable);
  }
  if (response.status !== 200 || typeof body?.request_id !== 'string' || !body.request_id ||
      typeof body.route_id !== 'string' || body.route_id.toLowerCase() !== attempt.routeID || !validRevision(body.revision)) {
    throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
  }
  if (attempt.operation === 'participation') {
    const value = body.participation;
    if (!['READY', 'UNCHANGED'].includes(body.status) || !value || typeof value.visit_id !== 'string' || value.visit_id.toLowerCase() !== attempt.visitID.toLowerCase() ||
        !['not_required', 'action_required', 'user_reported_confirmed', 'provider_confirmed', 'unavailable'].includes(value.status) || !['none', 'user', 'provider'].includes(value.evidence) ||
        value.status === 'provider_confirmed' && value.evidence !== 'provider' || value.status === 'user_reported_confirmed' && value.evidence !== 'user' ||
        !validRevision(value.updated_in_revision) || BigInt(value.updated_in_revision) > BigInt(body.revision) || !Number.isFinite(Date.parse(value.updated_at))) {
      throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
    }
    return body;
  }
  if (attempt.operation === 'execution') {
    const value = body.execution;
    if (!['READY', 'UNCHANGED'].includes(body.status) || !value || typeof value.visit_id !== 'string' || value.visit_id.toLowerCase() !== attempt.visitID.toLowerCase() ||
        !['planned', 'completed', 'skipped'].includes(value.status) || !['user_reported', 'provider_confirmed'].includes(value.confirmation_kind) ||
        !validRevision(value.updated_in_revision) || BigInt(value.updated_in_revision) > BigInt(body.revision) || !Number.isFinite(Date.parse(value.updated_at)) ||
        ['actual_started_at', 'actual_ended_at'].some((field) => value[field] !== undefined && !Number.isFinite(Date.parse(value[field]))) ||
        body.status === 'READY' && (value.status !== attempt.executionStatus || value.confirmation_kind !== 'user_reported')) {
      throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
    }
    return body;
  }
  if (
      !['READY', 'PARTIAL', 'CONFLICT', 'UNCHANGED'].includes(body.status) || !Array.isArray(body.conflicts) ||
      body.status !== 'CONFLICT' && body.conflicts.length || body.status === 'CONFLICT' && !body.conflicts.length ||
      attempt.operation === 'selection' && body.status !== 'CONFLICT' && body.revision !== attempt.revision) {
    throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
  }
  if (attempt.operation === 'pin' && (
      ['CONFLICT', 'UNCHANGED'].includes(body.status) && body.revision !== attempt.revision ||
      ['READY', 'PARTIAL'].includes(body.status) && BigInt(body.revision) !== BigInt(attempt.revision) + 1n ||
      body.conflicts.some((item) => typeof item?.code !== 'string' || typeof item.message !== 'string' || !item.message.trim() ||
        !Array.isArray(item.visit_ids) || item.visit_ids.some((id) => !uuid.test(id)) ||
        item.session_ids !== undefined && (!Array.isArray(item.session_ids) || item.session_ids.some((id) => !uuid.test(id)))))) {
    throw new RouteRequestError(0, 'INVALID_RESPONSE', true);
  }
  return body;
}

export function pinHistoryIncomplete(route) {
  const current = new Set(route.plan.steps.map((step) => step.visit_id));
  return route.execution?.some((item) => current.has(item.visit_id) && item.status === 'completed' &&
    (!Number.isFinite(Date.parse(item.actual_started_at)) || !Number.isFinite(Date.parse(item.actual_ended_at)) || Date.parse(item.actual_ended_at) <= Date.parse(item.actual_started_at))) || false;
}

export function createPinAttempt(route, visitID, kind, key = crypto.randomUUID()) {
  const base = createRouteAttempt('save', route, key);
  if (!uuid.test(visitID) || !['preferred', 'obligation', 'none'].includes(kind) ||
      !route.plan.steps.some((step) => step.visit_id === visitID && step.kind === 'visit') || pinHistoryIncomplete(route) ||
      route.execution?.some((item) => item.visit_id === visitID && ['completed', 'skipped'].includes(item.status))) {
    throw new RouteRequestError(0, 'INVALID_PIN', false);
  }
  return Object.freeze({ ...base, operation: 'pin', visitID, pinKind: kind,
    path: `/api/v1/routes/${base.routeID}/visits/${visitID.toLowerCase()}/pin`, body: JSON.stringify({ pin_kind: kind }),
  });
}

export function terminalRouteError(error) {
  return error instanceof RouteRequestError && error.status >= 400 && error.status < 500 && ![401, 408, 429].includes(error.status);
}
