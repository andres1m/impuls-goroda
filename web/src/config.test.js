import assert from 'node:assert/strict';
import { test } from 'node:test';
import { loadRuntimeConfig, parseRuntimeConfig } from './config.js';

test('accepts a secure API origin and local development HTTP', () => {
  assert.deepEqual(parseRuntimeConfig({ apiBaseUrl: 'https://api.example.org/' }), {
    apiBaseUrl: 'https://api.example.org',
    prototypeMode: false,
  });
  assert.deepEqual(parseRuntimeConfig({ apiBaseUrl: 'http://localhost:8080/' }), {
    apiBaseUrl: 'http://localhost:8080',
    prototypeMode: false,
  });
});

test('rejects unsafe or malformed API addresses', () => {
  for (const apiBaseUrl of [
    'http://api.example.org',
    'https://api.example.org/v1',
    'https://api.example.org/?token=secret',
    'https://user:pass@api.example.org',
    '/api',
  ]) {
    assert.throws(() => parseRuntimeConfig({ apiBaseUrl }), /Invalid API address/);
  }
});

test('loads runtime config without browser cache', async () => {
  const requested = [];
  const result = await loadRuntimeConfig('/config.json', async (...args) => {
    requested.push(args);
    return { ok: true, json: async () => ({ apiBaseUrl: 'https://api.example.org' }) };
  });
  assert.deepEqual(result, { apiBaseUrl: 'https://api.example.org', prototypeMode: false });
  assert.equal(requested[0][0], '/config.json');
  assert.equal(requested[0][1].cache, 'no-store');
});

test('enables prototype mode only with an explicit boolean', () => {
  assert.equal(parseRuntimeConfig({ apiBaseUrl: 'https://api.example.org', prototypeMode: true }).prototypeMode, true);
  assert.throws(() => parseRuntimeConfig({ apiBaseUrl: 'https://api.example.org', prototypeMode: 'true' }), /Invalid prototype mode/);
});

test('reports missing runtime config', async () => {
  await assert.rejects(
    loadRuntimeConfig('/config.json', async () => ({ ok: false })),
    /Runtime configuration is unavailable/,
  );
});
