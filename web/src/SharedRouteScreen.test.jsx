import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createElement } from 'react';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.MutationObserver = dom.window.MutationObserver;

const { cleanup, fireEvent, render, screen } = await import('@testing-library/react');
const { default: SharedRouteScreen } = await import('./SharedRouteScreen.jsx');

test('shared route remains anonymous until recipient explicitly copies it', async () => {
  const previousFetch = globalThis.fetch;
  const previousWebApp = window.WebApp;
  const calls = [];
  const money = { amount_minor: '0', currency: 'RUB' };
  const route = { revision: '1', city: 'perm', timezone: 'Asia/Yekaterinburg',
    start_at: '2026-10-01T08:00:00Z', end_at: '2026-10-01T16:00:00Z', result: 'READY',
    archetype_id: 'history_heritage', updated_at: '2026-09-30T08:00:00Z',
    cost: { known_personal: money, known_transport: money, program_amount: money, unknown_components: [] },
    warnings: [], issues: [], steps: [], legs: [] };
  window.WebApp = { initData: 'signed' };
  globalThis.fetch = async (url, options) => {
    calls.push(String(url));
    if (String(url).endsWith('/auth/max')) return { ok: true, status: 200, json: async () => ({ access_token: 'session', token_type: 'Bearer', expires_at: '2026-10-01T00:00:00Z' }) };
    if (String(url).endsWith('/copy')) {
      assert.equal(options.headers['If-Match'], '"1"');
      return { ok: false, status: 422, json: async () => ({ status: 'CONFLICT', request_id: 'request',
        conflicts: [{ code: 'OBLIGATION_UNAVAILABLE', message: 'Visit unavailable', visit_ids: [] }] }) };
    }
    return { ok: true, status: 200, headers: { get: () => '"1"' }, json: async () => ({ request_id: 'request', route }) };
  };
  try {
    render(createElement(SharedRouteScreen, { apiBaseUrl: 'https://api.example', token: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' }));
    await screen.findByRole('button', { name: 'Создать свой план' });
    assert.equal(calls.filter((url) => url.endsWith('/auth/max')).length, 0);
    fireEvent.click(screen.getByRole('button', { name: 'Создать свой план' }));
    fireEvent.change(screen.getByLabelText('Широта'), { target: { value: '58' } });
    fireEvent.change(screen.getByLabelText('Долгота'), { target: { value: '56' } });
    fireEvent.click(screen.getByRole('button', { name: 'Скопировать маршрут' }));
    await screen.findByText(/Сейчас этот маршрут скопировать нельзя/);
    assert.equal(calls.filter((url) => url.endsWith('/auth/max')).length, 1);
    assert.equal(calls.filter((url) => url.endsWith('/copy')).length, 1);
  } finally {
    cleanup(); globalThis.fetch = previousFetch; window.WebApp = previousWebApp;
  }
});
