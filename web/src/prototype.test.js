import assert from 'node:assert/strict';
import { test } from 'node:test';
import { prototypeScenarios, selectedPrototypeScenario } from './prototype.js';

test('selects only known synthetic scenarios from MAX launch or browser preview', () => {
  assert.equal(selectedPrototypeScenario({ initDataUnsafe: { start_param: 'demo_culture' } }, '?demo=vibe'), 'culture');
  assert.equal(selectedPrototypeScenario({}, '?demo=energy'), 'energy');
  assert.equal(selectedPrototypeScenario({ initDataUnsafe: { start_param: 'demo_shared_balance' } }, ''), 'balance');
  assert.equal(selectedPrototypeScenario({ initDataUnsafe: { start_param: 'demo_unknown' } }, ''), 'vibe');
  assert.equal(selectedPrototypeScenario({}, '?demo=__proto__'), 'vibe');
  assert.equal(Object.keys(prototypeScenarios).length, 7);
});
