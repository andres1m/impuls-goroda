const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const resultStatuses = new Set(['READY', 'PARTIAL', 'NO_FEASIBLE_ROUTE', 'CONFLICT']);
const dataModes = new Set(['live', 'prepared', 'synthetic']);

export class GatewayError extends Error {
  constructor(httpStatus, response) {
    super(response.message);
    this.name = 'GatewayError';
    this.httpStatus = httpStatus;
    this.code = response.code;
    this.requestId = response.request_id;
    this.retryable = response.retryable;
    this.currentVersion = response.current_version;
  }
}

function validErrorResponse(body) {
  return body &&
    typeof body.code === 'string' && /^[A-Z][A-Z0-9_]*$/.test(body.code) &&
    typeof body.message === 'string' && body.message.length > 0 &&
    typeof body.request_id === 'string' && body.request_id.length > 0 &&
    typeof body.retryable === 'boolean';
}

export function parseOptimizeResponse(body) {
  if (
    !body || !resultStatuses.has(body.status) ||
    typeof body.request_id !== 'string' || body.request_id.length === 0 ||
    !dataModes.has(body.data_mode) ||
    !Array.isArray(body.warnings) ||
    !Array.isArray(body.routes) ||
    !Array.isArray(body.conflicts) ||
    !Number.isInteger(body.computation_time_ms) || body.computation_time_ms < 0
  ) {
    throw new Error('Invalid route calculation response');
  }

  const kind = body.status === 'NO_FEASIBLE_ROUTE'
    ? 'no_feasible_route'
    : body.status === 'CONFLICT' ? 'conflict' : 'routes';
  return { ...body, kind };
}

export async function optimizeRoutes(apiBaseUrl, accessToken, input, idempotencyKey, fetcher = fetch, signal) {
  if (typeof accessToken !== 'string' || accessToken.length === 0) {
    throw new Error('Authorization is required');
  }
  if (!uuidPattern.test(idempotencyKey)) {
    throw new Error('Invalid idempotency key');
  }
  if (!input || typeof input !== 'object' || Array.isArray(input)) {
    throw new Error('Invalid route calculation input');
  }

  const response = await fetcher(`${apiBaseUrl}/api/v1/routes/optimize`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${accessToken}`,
      'Idempotency-Key': idempotencyKey,
    },
    body: JSON.stringify(input),
    credentials: 'omit',
    cache: 'no-store',
    signal,
  });

  let body;
  try {
    body = await response.json();
  } catch {
    throw new Error('Invalid route calculation response');
  }

  if (response.status === 200) return parseOptimizeResponse(body);
  if (!validErrorResponse(body)) throw new Error('Invalid route calculation response');
  throw new GatewayError(response.status, body);
}
