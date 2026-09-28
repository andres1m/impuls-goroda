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
const { default: ResultsView } = await import('./ResultsView.jsx');

const base = {
  data_mode: 'prepared', data_as_of: '2026-09-26T10:00:00Z', warnings: [], routes: [], conflicts: [],
};

test('renders a partial draft without treating unknown cost as free', () => {
  render(createElement(ResultsView, { state: { kind: 'result', result: {
    ...base,
    status: 'PARTIAL',
    warnings: [{ code: 'CHECK', message: 'Проверьте доступность.' }],
    routes: [{
      route_id: 'route-1', revision: '1', plan: {
        archetype_id: 'urban_avantgarde', start_at: '2026-09-26T12:00:00+05:00', end_at: '2026-09-26T16:00:00+05:00',
        cost: { known_personal: { amount_minor: '35000', currency: 'RUB' }, unknown_components: [{ code: 'UNKNOWN_TRANSIT', message: 'Стоимость перехода неизвестна.' }], budget_conclusion: 'unknown' },
      },
    }],
  } } }));

  assert.match(screen.getByRole('heading', { name: 'Найдена часть вариантов' }).textContent, /Найдена часть вариантов/);
  assert.match(screen.getByText(/Заранее полученные данные/).textContent, /2026-09-26/);
  assert.match(screen.getByText(/350,00 RUB/).textContent, /350,00 RUB/);
  assert.match(screen.getByText('Современный город').textContent, /Современный город/);
  assert.match(screen.getByText(/26\.09\.2026, 12:00 UTC\+05:00/).textContent, /UTC\+05:00/);
  assert.match(screen.getByText(/Стоимость перехода неизвестна/).textContent, /неизвестна/);
  assert.match(screen.getByText(/Проверьте доступность/).textContent, /Проверьте/);
  assert.match(screen.getByText(/Не подтверждён/).textContent, /Не подтверждён/);
  cleanup();
});

test('keeps a successful empty list explicit and never invents variants', () => {
  render(createElement(ResultsView, { state: { kind: 'result', result: { ...base, status: 'READY' } } }));
  assert.match(screen.getByText('В ответе нет вариантов маршрута.').textContent, /нет вариантов/);
  assert.equal(screen.queryByText(/Вариант 1/), null);
  cleanup();
});

test('shows domain refusals separately from HTTP errors', () => {
  const view = render(createElement(ResultsView, { state: { kind: 'result', result: {
    ...base, status: 'NO_FEASIBLE_ROUTE', warnings: [{ code: 'NO_ROUTE', message: 'Маршрут не укладывается во время.' }],
  } } }));
  assert.match(screen.getByRole('heading', { name: 'Подходящий маршрут не найден' }).textContent, /не найден/);
  assert.match(screen.getByText('Маршрут не укладывается во время.').textContent, /время/);
  view.rerender(createElement(ResultsView, { state: { kind: 'result', result: {
    ...base, status: 'CONFLICT', conflicts: [{ code: 'OVERLAP', message: 'Посещения пересекаются.' }],
  } } }));
  assert.match(screen.getByRole('heading', { name: 'Обязательные условия конфликтуют' }).textContent, /конфликтуют/);
  assert.match(screen.getByText('Посещения пересекаются.').textContent, /пересекаются/);
  cleanup();
});

test('renders 401, 409, 429 and 503 with retry only when allowed', () => {
  let retries = 0;
  const view = render(createElement(ResultsView, { state: { kind: 'loading' } }));
  assert.match(screen.getByRole('heading', { name: 'Рассчитываем маршрут…' }).textContent, /Рассчитываем/);
  for (const [httpStatus, code, title, retryable] of [
    [401, 'AUTH_REQUIRED', 'Требуется повторный вход', false],
    [409, 'REVISION_CONFLICT', 'Команда конфликтует с текущим состоянием', false],
    [429, 'RATE_LIMITED', 'Слишком много запросов', true],
    [503, 'UNAVAILABLE', 'Сервис временно недоступен', true],
  ]) {
    view.rerender(createElement(ResultsView, {
      state: { kind: 'error', error: { httpStatus, code, message: 'Запрос не выполнен.', retryable } },
      onRetry: () => { retries += 1; },
    }));
    assert.ok(screen.getByRole('heading', { name: title }));
    assert.match(screen.getByText(`Код: ${code}`).textContent, new RegExp(code));
    const retry = screen.queryByRole('button', { name: 'Повторить' });
    assert.equal(Boolean(retry), retryable);
    if (retry) fireEvent.click(retry);
  }
  assert.equal(retries, 2);
  cleanup();
});
