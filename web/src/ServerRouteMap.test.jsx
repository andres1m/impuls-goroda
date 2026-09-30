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

class FakeMap {
  on() {}
  fitBounds() {}
  setCenter() {}
  setZoom() {}
  destroy() {}
}
class FakeObject {
  destroy() {}
}
dom.window.mapgl = { Map: FakeMap, Polyline: FakeObject, HtmlMarker: FakeObject };

const { cleanup, render, screen, waitFor } = await import('@testing-library/react');
const { default: ServerRouteMap } = await import('./ServerRouteMap.jsx');

function projection(visitPoint = [58.01, 56.25]) {
  const leg = { position: 1, from_kind: 'origin', to_kind: 'visit', to_visit_id: 'v1', mode: 'walk', verification: 'estimated' };
  return { legs: [leg], segments: [], steps: [], execution: new Map(), origin: [58.0, 56.2], destination: null,
    visits: [{ id: 'v1', number: 1, title: 'Точка', point: visitPoint, completed: false }] };
}

const streetPath = { ok: true, status: 200, json: async () => ({ segments: [[[58.0, 56.2], [58.01, 56.25]]] }) };
const settle = () => new Promise((resolve) => setTimeout(resolve, 50));

async function withFetch(answers, run) {
  const previous = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push(JSON.parse(options.body));
    return answers.shift() ?? streetPath;
  };
  try {
    await run(calls);
  } finally {
    cleanup();
    globalThis.fetch = previous;
  }
}

const props = { apiKey: 'key', apiBaseUrl: 'https://api.example', onSelect() {} };

test('a reloaded route does not request street paths of unchanged legs again', async () => {
  await withFetch([], async (calls) => {
    const { rerender } = render(createElement(ServerRouteMap, { ...props, projection: projection() }));
    await screen.findByText(/Путь по улицам/);
    assert.equal(calls.length, 1);

    rerender(createElement(ServerRouteMap, { ...props, projection: projection() }));
    await settle();
    await screen.findByText(/Путь по улицам/);
    assert.equal(calls.length, 1, 'the second render of the same legs must reuse the first answer');
  });
});

test('a leg that moved is requested again', async () => {
  await withFetch([], async (calls) => {
    const { rerender } = render(createElement(ServerRouteMap, { ...props, projection: projection([58.05, 56.4]) }));
    await waitFor(() => assert.equal(calls.length, 1));
    rerender(createElement(ServerRouteMap, { ...props, projection: projection([58.06, 56.5]) }));
    await waitFor(() => assert.equal(calls.length, 2));
    assert.deepEqual(calls[1].points[1], [58.06, 56.5]);
  });
});

test('a failed request is not remembered, so the retry asks again', async () => {
  const limited = { ok: false, status: 503, json: async () => ({ code: 'DIRECTIONS_RATE_LIMITED' }) };
  await withFetch([limited], async (calls) => {
    const { rerender } = render(createElement(ServerRouteMap, { ...props, projection: projection([58.03, 56.31]) }));
    await screen.findByText(/Путь не загрузился/);
    rerender(createElement(ServerRouteMap, { ...props, projection: projection([58.03, 56.31]) }));
    await screen.findByText(/Путь по улицам/);
    assert.equal(calls.length, 2);
  });
});
