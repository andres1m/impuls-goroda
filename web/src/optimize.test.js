import assert from 'node:assert/strict';
import { test } from 'node:test';
import { GatewayError, optimizeRoutes } from './optimize.js';

const key = '11111111-1111-4111-8111-111111111111';
const baseResult = {
  request_id: 'request-1',
  data_mode: 'prepared',
  warnings: [],
  computation_time_ms: 18,
  routes: [],
  conflicts: [],
};

function reply(status, body) {
  return { status, json: async () => body };
}

test('sends an authenticated JSON command with the supplied idempotency key', async () => {
  const calls = [];
  const input = { city: 'moscow', constraints: { load_profile: 'provided-by-caller' } };
  const fetcher = async (...args) => {
    calls.push(args);
    return reply(200, { ...baseResult, status: 'READY', routes: [{ route_id: 'route-1' }] });
  };

  const first = await optimizeRoutes('https://api.example.org', 'session-token', input, key, fetcher);
  const second = await optimizeRoutes('https://api.example.org', 'session-token', input, key, fetcher);

  assert.equal(first.kind, 'routes');
  assert.equal(second.status, 'READY');
  assert.equal(calls.length, 2);
  for (const [url, options] of calls) {
    assert.equal(url, 'https://api.example.org/api/v1/routes/optimize');
    assert.equal(options.method, 'POST');
    assert.equal(options.headers.Authorization, 'Bearer session-token');
    assert.equal(options.headers['Idempotency-Key'], key);
    assert.equal(options.headers['Content-Type'], 'application/json');
    assert.equal(options.credentials, 'omit');
    assert.deepEqual(JSON.parse(options.body), input);
  }
});

test('keeps successful, partial and explained refusal statuses distinct', async () => {
  const cases = [
    ['PARTIAL', 'routes'],
    ['NO_FEASIBLE_ROUTE', 'no_feasible_route'],
    ['CONFLICT', 'conflict'],
  ];
  for (const [status, kind] of cases) {
    const result = await optimizeRoutes('https://api.example.org', 'token', {}, key, async () =>
      reply(200, { ...baseResult, status }),
    );
    assert.equal(result.kind, kind);
  }
});

test('preserves structured 401 and 409 errors separately from domain conflicts', async () => {
  for (const [status, code] of [[401, 'AUTH_REQUIRED'], [409, 'IDEMPOTENCY_KEY_REUSED']]) {
    await assert.rejects(
      optimizeRoutes('https://api.example.org', 'token', {}, key, async () => reply(status, {
        code,
        message: 'Request rejected',
        request_id: 'request-2',
        retryable: false,
      })),
      (error) => error instanceof GatewayError &&
        error.httpStatus === status && error.code === code &&
        error.requestId === 'request-2' && error.retryable === false,
    );
  }
});

test('rejects malformed responses without exposing request contents', async () => {
  await assert.rejects(
    optimizeRoutes('https://api.example.org', 'token', {}, key, async () =>
      reply(200, { ...baseResult, status: 'UNKNOWN' }),
    ),
    /Invalid route calculation response/,
  );
  await assert.rejects(
    optimizeRoutes('https://api.example.org', 'token', {}, key, async () => reply(503, { code: 'ERROR' })),
    /Invalid route calculation response/,
  );
});

test('rejects missing credentials and keys before sending', async () => {
  const noRequest = () => { throw new Error('request must not be sent'); };
  await assert.rejects(optimizeRoutes('https://api.example.org', '', {}, key, noRequest), /Authorization/);
  await assert.rejects(optimizeRoutes('https://api.example.org', 'token', {}, 'new-key', noRequest), /idempotency/);
});
