import { RouteRequestError } from './route.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const version = (value) => typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
const revision = (value) => version(value) && value !== '0';
const invalid = () => new RouteRequestError(0, 'INVALID_RESPONSE', true);

export function createNotificationAttempt(route, preference, enabled, key = crypto.randomUUID()) {
  if (!uuid.test(route?.route_id) || route.lifecycle !== 'saved' || !revision(route.revision) ||
      !version(preference?.version) || typeof enabled !== 'boolean' || !uuid.test(key)) {
    throw new RouteRequestError(0, 'INVALID_NOTIFICATION_PREFERENCE', false);
  }
  return Object.freeze({ routeID: route.route_id.toLowerCase(), revision: route.revision, key, enabled,
    expectedVersion: preference.version, body: JSON.stringify({ enabled, expected_version: preference.version }) });
}

async function request(apiBaseUrl, accessToken, routeID, attempt, fetcher, signal) {
  if (!accessToken) throw new RouteRequestError(401, 'AUTH_REQUIRED', false);
  if (!uuid.test(routeID)) throw invalid();
  let response;
  try {
    response = await fetcher(`${apiBaseUrl}/api/v1/routes/${routeID}/notifications`, {
      method: attempt ? 'POST' : 'GET', credentials: 'omit', cache: 'no-store', redirect: 'error', referrerPolicy: 'no-referrer', signal,
      headers: { Authorization: `Bearer ${accessToken}`, ...(attempt ? {
        'Content-Type': 'application/json', 'Idempotency-Key': attempt.key, 'If-Match': `"${attempt.revision}"`,
      } : {}) }, ...(attempt ? { body: attempt.body } : {}),
    });
  } catch (error) {
    if (signal?.aborted || error?.name === 'AbortError') throw new DOMException('Request cancelled', 'AbortError');
    throw new RouteRequestError(0, 'NETWORK_ERROR', true);
  }
  let body;
  try { body = await response.json(); } catch { throw invalid(); }
  if (!response.ok) {
    if (typeof body?.code !== 'string' || !/^[A-Z][A-Z0-9_]*$/.test(body.code) || typeof body.retryable !== 'boolean') throw invalid();
    throw new RouteRequestError(response.status, body.code, body.retryable);
  }
  const preference = body?.preference;
  if (response.status !== 200 || body?.route_id !== routeID.toLowerCase() || !revision(body.revision) ||
      typeof body.request_id !== 'string' || !body.request_id || !preference || Array.isArray(preference) ||
      typeof preference.enabled !== 'boolean' || !version(preference.version) ||
      preference.version === '0' && preference.enabled ||
      !['unknown', 'available', 'stopped', 'muted'].includes(preference.platform_state) ||
      Object.keys(preference).some((key) => !['enabled', 'version', 'platform_state'].includes(key)) ||
      Object.keys(body).some((key) => !['request_id', 'route_id', 'revision', 'preference'].includes(key)) ||
      response.headers.get('ETag') !== `"${body.revision}"`) throw invalid();
  if (attempt && (body.revision !== attempt.revision || preference.enabled !== attempt.enabled ||
      ![BigInt(attempt.expectedVersion), BigInt(attempt.expectedVersion) + 1n].includes(BigInt(preference.version)))) throw invalid();
  return body;
}

export function loadNotificationPreference(apiBaseUrl, accessToken, routeID, fetcher = fetch, signal) {
  return request(apiBaseUrl, accessToken, routeID, null, fetcher, signal);
}

export function sendNotificationPreference(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  return request(apiBaseUrl, accessToken, attempt.routeID, attempt, fetcher, signal);
}
