import assert from 'node:assert/strict';
import { test } from 'node:test';
import { exchangeMaxInitData } from './auth.js';

test('exchanges MAX launch data for an in-memory session', async () => {
  const calls = [];
  const session = await exchangeMaxInitData('https://api.example.org', 'signed-data', async (...args) => {
    calls.push(args);
    return {
      ok: true,
      json: async () => ({
        access_token: 'access-token',
        token_type: 'Bearer',
        expires_at: '2026-09-26T12:00:00Z',
      }),
    };
  });

  assert.deepEqual(session, {
    accessToken: 'access-token',
    expiresAt: '2026-09-26T12:00:00Z',
  });
  assert.equal(calls[0][0], 'https://api.example.org/api/v1/auth/max');
  assert.equal(calls[0][1].method, 'POST');
  assert.deepEqual(JSON.parse(calls[0][1].body), { init_data: 'signed-data' });
  assert.equal(calls[0][1].credentials, 'omit');
});

test('rejects missing launch data without a request', async () => {
  await assert.rejects(
    exchangeMaxInitData('https://api.example.org', '', () => {
      throw new Error('request must not be sent');
    }),
    /MAX launch data is unavailable/,
  );
});

test('rejects failed and malformed authorization responses', async () => {
  await assert.rejects(
    exchangeMaxInitData('https://api.example.org', 'signed-data', async () => ({ ok: false })),
    /MAX authorization failed/,
  );
  await assert.rejects(
    exchangeMaxInitData('https://api.example.org', 'signed-data', async () => ({
      ok: true,
      json: async () => ({ access_token: 'token', token_type: 'Basic' }),
    })),
    /Invalid authorization response/,
  );
});
