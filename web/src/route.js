export class RouteRequestError extends Error {
  constructor(status, code, retryable) {
    super(code);
    this.name = 'RouteRequestError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

async function readJson(response) {
  try {
    return await response.json();
  } catch {
    throw new RouteRequestError(response.status, 'INVALID_RESPONSE', false);
  }
}

async function get(apiBaseUrl, path, accessToken, fetcher, signal) {
  const response = await fetcher(`${apiBaseUrl}${path}`, {
    method: 'GET',
    headers: { Authorization: `Bearer ${accessToken}` },
    credentials: 'omit',
    cache: 'no-store',
    signal,
  });
  const body = await readJson(response);
  if (!response.ok) {
    throw new RouteRequestError(response.status, body.code || 'REQUEST_FAILED', body.retryable === true);
  }
  return body;
}

export async function loadSelectedRoute(apiBaseUrl, accessToken, fetcher = fetch, signal) {
  const context = await get(apiBaseUrl, '/api/v1/me/context', accessToken, fetcher, signal);
  if (!context || typeof context !== 'object' || !('confirmed_input' in context)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  if (!context.selected_route_id) return null;
  return loadOwnerRoute(apiBaseUrl, accessToken, context.selected_route_id, fetcher, signal);
}

export async function loadOwnerRoute(apiBaseUrl, accessToken, id, fetcher = fetch, signal) {
  if (!/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(id)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  const body = await get(apiBaseUrl, `/api/v1/routes/${id}`, accessToken, fetcher, signal);
  if (!body?.route?.plan || typeof body.route.route_id !== 'string' || body.route.route_id.toLowerCase() !== id.toLowerCase() || !Array.isArray(body.route.plan.steps) ||
      typeof body.route.revision !== 'string' || !/^[1-9][0-9]{0,18}$/.test(body.route.revision) || BigInt(body.route.revision) > 9223372036854775807n ||
      !['draft', 'saved'].includes(body.route.lifecycle)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  return body.route;
}
