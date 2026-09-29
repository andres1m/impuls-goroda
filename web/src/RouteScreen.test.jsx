import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createElement } from 'react';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>');
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.MutationObserver = dom.window.MutationObserver;

const { cleanup, fireEvent, render, screen } = await import('@testing-library/react');
const { default: RouteScreen } = await import('./RouteScreen.jsx');

test('renders real route steps and keeps unknown costs and prepared data explicit', () => {
  const route = {
    route_id: '11111111-1111-4111-8111-111111111111', city: 'perm', lifecycle: 'draft',
    plan: {
      timezone: 'Asia/Yekaterinburg', start_at: '2026-09-26T07:00:00Z', end_at: '2026-09-26T11:00:00Z',
      result: 'PARTIAL', warnings: [{ code: 'COST_UNKNOWN', message: 'Стоимость уточняется у организатора' }],
      cost: { known_personal: { amount_minor: '0', currency: 'RUB' }, unknown_components: [{ kind: 'ticket' }] },
      steps: [{ visit_id: '22222222-2222-4222-8222-222222222222', kind: 'visit', position: 1,
        visit_start_at: '2026-09-26T07:30:00Z', visit_end_at: '2026-09-26T08:30:00Z',
        catalog: { title: 'Название из каталога', category: 'culture', availability: 'unknown', data_mode: 'prepared' },
        cost: { unknown_components: [{ kind: 'ticket' }] } }],
    },
  };
  render(createElement(RouteScreen, { route }));
  assert.ok(screen.getByRole('heading', { name: 'По пути' }));
  fireEvent.click(screen.getByRole('button', { name: /Название из каталога/ }));
  assert.match(document.body.textContent, /Название из каталога/);
  assert.match(document.body.textContent, /Доступность неизвестна/);
  assert.match(document.body.textContent, /Подготовленные данные/);
  assert.ok(screen.getByLabelText('Есть неучтённые расходы'));
  assert.match(document.body.textContent, /12:30/);
  cleanup();
});
