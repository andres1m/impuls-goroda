import assert from 'node:assert/strict';
import { test } from 'node:test';
import { interestMask } from './taxonomy.js';

test('interest mask preserves the accepted bit positions as a fixed-width string', () => {
  assert.equal(interestMask([]), '0x0000000000000000');
  assert.equal(interestMask(['contemporary_art', 'cinema']), '0x0000000000001001');
});

test('interest mask rejects unknown or repeated codes', () => {
  assert.throws(() => interestMask(['unknown']), /интересы/);
  assert.throws(() => interestMask(['cinema', 'cinema']), /интересы/);
});
