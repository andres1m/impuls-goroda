import { RouteRequestError } from './route.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const revision = (value) => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;
const date = (value) => typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(value) && Number.isFinite(Date.parse(value));
const invalid = () => new RouteRequestError(0, 'INVALID_RESPONSE', true);
const sharedFields = new Set(['revision', 'city', 'timezone', 'start_at', 'end_at', 'result', 'archetype_id', 'cost', 'warnings', 'issues', 'steps', 'legs', 'updated_at']);
const privateFields = new Set(['route_id', 'owner_id', 'user_id', 'origin', 'destination', 'constraints', 'execution', 'private_reference', 'actual_started_at', 'actual_ended_at', 'source_record_id', 'source_change_id', 'copied_from', 'share_token']);

export function validShareToken(value) {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value)) return false;
  try {
    const bytes = atob(value.replaceAll('-', '+').replaceAll('_', '/') + '=');
    return bytes.length === 32 && btoa(bytes).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '') === value;
  } catch { return false; }
}

export function createShareAttempt(route, expiresAt, key = crypto.randomUUID()) {
  const target = shareTarget(route, key);
  if (expiresAt !== undefined && !date(expiresAt)) throw new RouteRequestError(0, 'INVALID_SHARE', false);
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  const token = btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
  return Object.freeze({ ...target, operation: 'create', token,
    body: JSON.stringify({ share_token: token, ...(expiresAt === undefined ? {} : { expires_at: new Date(expiresAt).toISOString() }) }),
  });
}

export function createRevokeShareAttempt(route, key = crypto.randomUUID()) {
  return Object.freeze({ ...shareTarget(route, key), operation: 'revoke' });
}

function shareTarget(route, key) {
  if (!uuid.test(route?.route_id) || !revision(route.revision) || !uuid.test(key)) {
    throw new RouteRequestError(0, 'INVALID_SHARE', false);
  }
  const routeID = route.route_id.toLowerCase();
  return { routeID, revision: route.revision, key, path: `/api/v1/routes/${routeID}/share` };
}

async function shareFetch(url, options, fetcher) {
  try {
    return await fetcher(url, { credentials: 'omit', cache: 'no-store', referrerPolicy: 'no-referrer', redirect: 'error', ...options });
  } catch (error) {
    if (error?.name === 'AbortError') throw new DOMException('Request cancelled', 'AbortError');
    throw new RouteRequestError(0, 'NETWORK_ERROR', true);
  }
}

async function shareBody(response) {
  let body;
  try { body = await response.json(); } catch { throw invalid(); }
  if (!response.ok) {
    if (typeof body?.code !== 'string' || !/^[A-Z][A-Z0-9_]*$/.test(body.code) || typeof body.retryable !== 'boolean') throw invalid();
    throw new RouteRequestError(response.status, body.code, body.retryable);
  }
  if (response.status !== 200 || typeof body?.request_id !== 'string' || !body.request_id) throw invalid();
  return body;
}

export async function sendShareCommand(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  if (!accessToken) throw new RouteRequestError(401, 'AUTH_REQUIRED', false);
  if (!attempt || !['create', 'revoke'].includes(attempt.operation)) throw new RouteRequestError(0, 'INVALID_SHARE', false);
  const response = await shareFetch(`${apiBaseUrl}${attempt.path}`, {
    method: attempt.operation === 'create' ? 'POST' : 'DELETE', signal,
    headers: { Authorization: `Bearer ${accessToken}`, 'Idempotency-Key': attempt.key, 'If-Match': `"${attempt.revision}"`,
      ...(attempt.operation === 'create' ? { 'Content-Type': 'application/json' } : {}),
    }, ...(attempt.operation === 'create' ? { body: attempt.body } : {}),
  }, fetcher);
  if (attempt.operation === 'revoke' && response.status === 204) return { status: 'REVOKED' };
  const body = await shareBody(response);
  if (attempt.operation !== 'create' || !['READY', 'UNCHANGED'].includes(body.status) || body.revision !== attempt.revision ||
      body.share_token !== attempt.token || !validShareToken(body.share_token) || !date(body.created_at) ||
      body.expires_at !== undefined && (!date(body.expires_at) || Date.parse(body.expires_at) <= Date.parse(body.created_at)) ||
      response.headers.get('ETag') !== `"${body.revision}"`) throw invalid();
  let link;
  try { link = new URL(body.deep_link); } catch { throw invalid(); }
  if (link.protocol !== 'https:' || link.host !== 'max.ru' || link.username || link.password || link.hash ||
      !/^\/[^/]+$/.test(link.pathname) || link.searchParams.getAll('startapp').length !== 1 ||
      link.searchParams.get('startapp') !== attempt.token || [...link.searchParams.keys()].some((key) => key !== 'startapp')) throw invalid();
  return body;
}

export async function loadSharedRoute(apiBaseUrl, token, fetcher = fetch, signal) {
  if (!validShareToken(token)) throw new RouteRequestError(404, 'SHARE_NOT_FOUND', false);
  const response = await shareFetch(`${apiBaseUrl}/api/v1/shared-routes/${token}`, { method: 'GET', signal }, fetcher);
  const body = await shareBody(response);
  const route = body.route;
  if (!route || typeof route !== 'object' || Array.isArray(route) || Object.keys(route).some((key) => !sharedFields.has(key)) ||
      !revision(route.revision) || !['READY', 'PARTIAL'].includes(route.result) ||
      ['city', 'timezone', 'archetype_id'].some((key) => typeof route[key] !== 'string' || !route[key]) ||
      !date(route.start_at) || !date(route.end_at) || Date.parse(route.end_at) <= Date.parse(route.start_at) || !date(route.updated_at) ||
      !route.cost || typeof route.cost !== 'object' || Array.isArray(route.cost) ||
      ['warnings', 'issues', 'steps', 'legs'].some((key) => !Array.isArray(route[key])) ||
      response.headers.get('ETag') !== `"${route.revision}"` || !publicData(route)) throw invalid();
  if (route.warnings.some((item) => !item || typeof item.code !== 'string' || typeof item.message !== 'string') ||
      route.issues.some((item) => !item || !uuid.test(item.issue_id) || typeof item.message !== 'string' || !['open', 'acknowledged', 'resolved'].includes(item.state)) ||
      !validMoney(route.cost.known_personal) || !validMoney(route.cost.known_transport) || !validMoney(route.cost.program_amount) ||
      !Array.isArray(route.cost.unknown_components)) throw invalid();
  const visits = new Set();
  for (const step of route.steps) {
    if (!step || !uuid.test(step.visit_id) || visits.has(step.visit_id) || !['visit', 'free_time'].includes(step.kind) ||
        !Number.isInteger(step.position) || step.position !== visits.size + 1 ||
        ['arrival_at', 'visit_start_at', 'visit_end_at', 'departure_at'].some((key) => !date(step[key])) ||
        !Array.isArray(step.applied_constraints) || step.applied_constraints.some((item) => !item || typeof item.message !== 'string') ||
        step.kind === 'visit' && (!step.catalog || typeof step.catalog.title !== 'string' ||
          ['category', 'availability', 'data_mode', 'registration_details', 'age_requirements'].some((key) => step.catalog[key] !== undefined && typeof step.catalog[key] !== 'string')) ||
        step.cost != null && (!Array.isArray(step.cost.unknown_components) || step.cost.personal_amount !== undefined && !validMoney(step.cost.personal_amount))) throw invalid();
    visits.add(step.visit_id);
  }
  let position = 0;
  for (const leg of route.legs) {
    if (!leg || leg.from_kind !== 'visit' || leg.to_kind !== 'visit' || !visits.has(leg.from_visit_id) || !visits.has(leg.to_visit_id) ||
        !Number.isInteger(leg.position) || leg.position <= position || 'evidence' in leg || !date(leg.departure_at) || !date(leg.arrival_at) ||
        !['walk', 'transit', 'car'].includes(leg.mode) || !['verified', 'estimated', 'unknown', 'unavailable'].includes(leg.verification) ||
        leg.geometry !== undefined && (!Array.isArray(leg.geometry) || leg.geometry.some((point) => !point ||
          !Number.isFinite(point.latitude) || Math.abs(point.latitude) > 90 || !Number.isFinite(point.longitude) || Math.abs(point.longitude) > 180))) throw invalid();
    position = leg.position;
  }
  return route;
}

function validMoney(value) {
  return value && typeof value.currency === 'string' && value.currency.length > 0 &&
    typeof value.amount_minor === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value.amount_minor) && BigInt(value.amount_minor) <= 9223372036854775807n;
}

function publicData(value, depth = 0) {
  if (depth > 30) return false;
  if (value === null || typeof value !== 'object') return true;
  return Object.entries(value).every(([key, nested]) => !privateFields.has(key.toLowerCase()) && publicData(nested, depth + 1));
}
