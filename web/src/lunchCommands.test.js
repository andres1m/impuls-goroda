import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createLunchAttempt, sendLunchCommand } from './lunchCommands.js';

const id = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const route = { route_id: id(1), revision: '2', lifecycle: 'saved', plan: { steps: [
  { visit_id: id(2), kind: 'visit' }, { visit_id: id(3), kind: 'external_lunch', lunch: { after_visit_id: id(2), duration_seconds: 2700 }, external_venue: { provider: '2gis', external_id: 'cafe' } },
] } };

test('add attempt freezes placement, venue, revision, and idempotency key', () => {
  const attempt = createLunchAttempt(route, { action: 'add', placement: { after_visit_id: id(2), duration_seconds: 3600 }, venue: { provider: '2gis', external_id: 'new' } }, id(4));
  assert.equal(attempt.key, id(4));
  assert.equal(attempt.revision, '2');
  assert.deepEqual(JSON.parse(attempt.body), { action: 'add', placement: { after_visit_id: id(2), duration_seconds: 3600 }, venue: { provider: '2gis', external_id: 'new' } });
  assert(Object.isFrozen(attempt));
});

test('update and remove require existing lunch and explicit acknowledgement', () => {
  assert.throws(() => createLunchAttempt(route, { action: 'remove', lunch_id: id(3) }));
  assert.equal(JSON.parse(createLunchAttempt(route, { action: 'remove', lunch_id: id(3), acknowledge_external_commitment: false }, id(4)).body).action, 'remove');
  assert.throws(() => createLunchAttempt(route, { action: 'update', lunch_id: id(9), placement: { after_visit_id: id(2), duration_seconds: 2700 }, acknowledge_external_commitment: true }));
});

test('sends frozen attempt with optimistic revision and preserves conflict response', async () => {
  const attempt = createLunchAttempt(route, { action: 'remove', lunch_id: id(3), acknowledge_external_commitment: false }, id(4));
  const result = await sendLunchCommand('https://api.example', 'token', attempt, async (url, options) => {
    assert.equal(url, `https://api.example/api/v1/routes/${id(1)}/lunch/proposals`);
    assert.equal(options.headers['If-Match'], '"2"');
    assert.equal(options.headers['Idempotency-Key'], id(4));
    return { ok: true, status: 200, json: async () => ({ status: 'CONFLICT', request_id: 'req', route_id: id(1), revision: '2', changes: [], conflicts: [{ code: 'NO_SLOT', message: 'No slot', visit_ids: [] }] }) };
  });
  assert.equal(result.status, 'CONFLICT');
});

test('moving an existing external lunch keeps its venue unless free time is explicitly chosen', async () => {
  const { venueForLunch } = await import('./lunchCommands.js');
  assert.deepEqual(venueForLunch(route.plan.steps[1], null, false), { provider: '2gis', external_id: 'cafe' });
  assert.equal(venueForLunch(route.plan.steps[1], null, true), undefined);
});

test('moving a catalog lunch uses its existing visit ID', async () => {
  const { venueForLunch } = await import('./lunchCommands.js');
  assert.deepEqual(venueForLunch({ visit_id: id(5), kind: 'visit', catalog: { title: 'Cafe' }, lunch: {} }, null, false), { catalog_visit_id: id(5) });
});
