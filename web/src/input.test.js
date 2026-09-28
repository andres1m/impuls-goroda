import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validatePlanInput, zonedDateTime } from './input.js';

function form(city) {
  return {
    city,
    startAt: '2026-09-26T12:00',
    endAt: '2026-09-26T16:00',
    latitude: '55.75',
    longitude: '37.62',
    interests: ['running_park'],
    movementModes: ['walk', 'transit'],
    excludedCategories: [],
  };
}

test('the same local time means different instants in Moscow and Perm', () => {
  const moscow = validatePlanInput(form('moscow'));
  const perm = validatePlanInput(form('perm'));
  assert.equal(moscow.startAt, '2026-09-26T12:00:00+03:00');
  assert.equal(perm.startAt, '2026-09-26T12:00:00+05:00');
  assert.equal(Date.parse(moscow.startAt) - Date.parse(perm.startAt), 2 * 60 * 60 * 1000);
  assert.equal(moscow.interestMask, '0x0000000000000010');
  assert.deepEqual(moscow.movementModes, ['walk', 'transit']);
});

test('rejects reversed time windows and invalid manual coordinates', () => {
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), endAt: '2026-09-26T11:00' }),
    /позже начала/,
  );
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), latitude: '91' }),
    /широту/,
  );
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), longitude: '' }),
    /координаты/,
  );
});

test('rejects nonexistent dates instead of normalizing them', () => {
  assert.throws(
    () => zonedDateTime('2026-02-30T12:00', 'Europe/Moscow'),
    /недоступно/,
  );
});

test('requires a supported movement mode and validates excluded categories', () => {
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), movementModes: [] }),
    /способ передвижения/,
  );
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), movementModes: ['car'] }),
    /способ передвижения/,
  );
  assert.throws(
    () => validatePlanInput({ ...form('moscow'), excludedCategories: ['unknown'] }),
    /категории/,
  );
  assert.deepEqual(
    validatePlanInput({ ...form('moscow'), excludedCategories: ['culture', 'sport'] }).excludedCategories,
    ['culture', 'sport'],
  );
});
