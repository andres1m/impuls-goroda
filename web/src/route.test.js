import assert from 'node:assert/strict';
import { test } from 'node:test';
import { loadSelectedRoute, RouteRequestError } from './route.js';

const selectedID = '11111111-1111-4111-8111-111111111111';
const context = { confirmed_input: {}, selected_route_id: selectedID };
const route = { route_id: selectedID, plan: { steps: [] } };

test('loads only the selected owner route with bearer authorization', async () => {
  const calls = [];
  const fetcher = async (url, options) => {
    calls.push({ url, options });
    return { ok: true, status: 200, json: async () => calls.length === 1 ? context : { route } };
  };
  assert.deepEqual(await loadSelectedRoute('https://api.example.org', 'session', fetcher), route);
  assert.deepEqual(calls.map((call) => call.url), [
    'https://api.example.org/api/v1/me/context',
    `https://api.example.org/api/v1/routes/${selectedID}`,
  ]);
  assert.ok(calls.every((call) => call.options.headers.Authorization === 'Bearer session'));
  assert.ok(calls.every((call) => call.options.credentials === 'omit'));
});

test('does not request a route when no route is selected', async () => {
  let calls = 0;
  const fetcher = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({ confirmed_input: {} }) };
  };
  assert.equal(await loadSelectedRoute('https://api.example.org', 'session', fetcher), null);
  assert.equal(calls, 1);
});

test('rejects an invalid selected ID before constructing a URL', async () => {
  let calls = 0;
  const fetcher = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({ confirmed_input: {}, selected_route_id: '../other' }) };
  };
  await assert.rejects(loadSelectedRoute('https://api.example.org', 'session', fetcher),
    (error) => error instanceof RouteRequestError && error.code === 'INVALID_RESPONSE');
  assert.equal(calls, 1);
});

test('preserves authorization failure without requesting a route', async () => {
  const fetcher = async () => ({ ok: false, status: 401, json: async () => ({ code: 'AUTH_REQUIRED', retryable: false }) });
  await assert.rejects(loadSelectedRoute('https://api.example.org', 'session', fetcher),
    (error) => error instanceof RouteRequestError && error.status === 401 && error.code === 'AUTH_REQUIRED');
});
