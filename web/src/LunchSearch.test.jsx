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
dom.window.HTMLElement.prototype.scrollIntoView = () => {};

const { cleanup, fireEvent, render, screen } = await import('@testing-library/react');
const { default: LunchSearch } = await import('./LunchSearch.jsx');

test('searches from a chosen point when device geolocation is unavailable', async () => {
  const previousFetch = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (_url, options) => {
    calls.push(JSON.parse(options.body));
    return { ok: true, json: async () => ({ request_id: 'request', searched_at: new Date().toISOString(),
      radius_meters: calls.at(-1).radius_meters, candidates: [] }) };
  };
  try {
    render(createElement(LunchSearch, { routeID: 'route', city: 'perm', plan: { origin: { latitude: 58, longitude: 56 } },
      apiBaseUrl: 'https://api.example', accessToken: 'session' }));
    fireEvent.click(screen.getByRole('button', { name: 'Обед · кафе рядом' }));
    await screen.findByText(/Геопозиция недоступна/);
    fireEvent.change(screen.getByLabelText('Широта'), { target: { value: '58.01' } });
    fireEvent.change(screen.getByLabelText('Долгота'), { target: { value: '56.25' } });
    fireEvent.click(screen.getByRole('button', { name: 'Искать от точки' }));
    await screen.findByText(/В этом радиусе ничего не найдено/);
    assert.deepEqual(calls[0], { position: { latitude: 58.01, longitude: 56.25 }, radius_meters: 500 });
    fireEvent.click(screen.getByRole('button', { name: '800 м' }));
    await screen.findByText(/В этом радиусе ничего не найдено/);
    assert.deepEqual(calls[1], { position: { latitude: 58.01, longitude: 56.25 }, radius_meters: 800 });
  } finally {
    cleanup(); globalThis.fetch = previousFetch;
  }
});
