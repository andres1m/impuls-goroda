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

const { cleanup, render, screen } = await import('@testing-library/react');
const { default: App } = await import('./App.jsx');

test('opens on the selected-route state instead of the input form', async () => {
  const previousFetch = globalThis.fetch;
  const previousWebApp = window.WebApp;
  const requested = [];
  window.WebApp = { initData: 'signed-launch-data' };
  globalThis.fetch = async (url) => {
    requested.push(String(url));
    if (String(url).endsWith('/config.json')) {
      return { ok: true, status: 200, json: async () => ({ apiBaseUrl: 'https://api.example.org' }) };
    }
    if (String(url).endsWith('/auth/max')) {
      return { ok: true, status: 200, json: async () => ({ access_token: 'session', token_type: 'Bearer', expires_at: '2026-09-27T00:00:00Z' }) };
    }
    if (String(url).endsWith('/api/v1/me/context')) {
      return { ok: true, status: 200, json: async () => ({ confirmed_input: {} }) };
    }
    return { ok: true, status: 200, json: async () => ({ request_id: 'test', scenario: null }) };
  };

  try {
    render(createElement(App));
    await screen.findByRole('heading', { name: 'Как проведём день?' });
    assert.equal(screen.queryByRole('heading', { name: 'Соберите день под себя' }), null);
    assert.equal(requested.filter((url) => url.endsWith('/api/v1/me/context')).length, 1);
  } finally {
    cleanup();
    globalThis.fetch = previousFetch;
    window.WebApp = previousWebApp;
  }
});
