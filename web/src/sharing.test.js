import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createCopyAttempt, sendCopyCommand } from './sharing.js';

const token = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';
const key = '11111111-1111-4111-8111-111111111111';
const shared = { revision: '3', steps: [{ visit_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' }] };

test('copy request uses the selected shared revision and recipient origin', async () => {
  const attempt = createCopyAttempt(shared, token, { latitude: 58, longitude: 56 }, [], key);
  let sent;
  const result = await sendCopyCommand('https://api.example', 'session', attempt, async (url, options) => {
    sent = { url, options };
    return { ok: true, status: 200, headers: { get: () => '"1"' }, json: async () => ({
      status: 'READY', request_id: 'request', route: { route_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', revision: '1', lifecycle: 'draft',
        plan: { origin: { latitude: 58, longitude: 56 }, steps: [{ visit_id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc' }] } },
    }) };
  });
  assert.equal(sent.url, `https://api.example/api/v1/shared-routes/${token}/copy`);
  assert.equal(sent.options.headers['If-Match'], '"3"');
  assert.deepEqual(JSON.parse(sent.options.body), { origin: { latitude: 58, longitude: 56 }, accepted_unknowns: [] });
  assert.equal(result.route.lifecycle, 'draft');
});

test('copy refusal exposes conflicts and no draft', async () => {
  const attempt = createCopyAttempt(shared, token, { latitude: 58, longitude: 56 }, [], key);
  const result = await sendCopyCommand('https://api.example', 'session', attempt, async () => ({
    ok: false, status: 422, json: async () => ({ status: 'CONFLICT', request_id: 'request',
      conflicts: [{ code: 'OBLIGATION_UNAVAILABLE', message: 'Visit changed', visit_ids: [] }] }),
  }));
  assert.equal(result.status, 'CONFLICT');
  assert.equal(result.route, undefined);
  assert.equal(result.conflicts[0].code, 'OBLIGATION_UNAVAILABLE');
});
