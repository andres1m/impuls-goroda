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
const { default: PlanForm } = await import('./PlanForm.jsx');

test('keeps the entered conditions while moving forward and back', () => {
  const view = render(createElement(PlanForm));

  fireEvent.click(screen.getByRole('button', { name: 'К ограничениям' }));
  assert.match(screen.getByRole('alert').textContent, /Укажите дату и время/);
  assert.equal(document.activeElement, screen.getByRole('alert'));

  fireEvent.click(screen.getByRole('radio', { name: 'Пермь' }));
  fireEvent.change(screen.getByLabelText('Начало'), { target: { value: '2026-09-26T12:00' } });
  fireEvent.change(screen.getByLabelText('Конец'), { target: { value: '2026-09-26T16:00' } });
  fireEvent.change(screen.getByLabelText('Широта'), { target: { value: '58.01' } });
  fireEvent.change(screen.getByLabelText('Долгота'), { target: { value: '56.23' } });
  fireEvent.click(screen.getByRole('button', { name: 'К ограничениям' }));

  assert.equal(screen.getByRole('heading', { name: 'Интересы и ограничения' }).textContent, 'Интересы и ограничения');
  assert.equal(document.activeElement, screen.getByRole('heading', { name: 'Интересы и ограничения' }));
  fireEvent.click(screen.getByRole('button', { name: 'Проверить условия' }));
  assert.match(screen.getByRole('alert').textContent, /способ передвижения/);
  fireEvent.click(screen.getByLabelText('Пешком'));
  fireEvent.click(screen.getByLabelText('Современное искусство'));
  fireEvent.click(screen.getByRole('button', { name: 'Проверить условия' }));

  const review = screen.getByRole('region', { name: 'Проверка условий' });
  assert.match(review.textContent, /Пермь/);
  assert.match(review.textContent, /26\.09\.2026, 12:00 UTC\+05:00/);
  assert.match(review.textContent, /Пешком/);
  assert.match(review.textContent, /Современное искусство/);

  fireEvent.click(screen.getByRole('button', { name: 'Назад' }));
  assert.equal(screen.getByLabelText('Пешком').checked, true);
  assert.equal(screen.getByLabelText('Современное искусство').checked, true);
  fireEvent.click(screen.getByRole('button', { name: 'Назад' }));
  assert.equal(screen.getByRole('radio', { name: 'Пермь' }).checked, true);
  assert.equal(screen.getByLabelText('Начало').value, '2026-09-26T12:00');
  assert.equal(screen.getByLabelText('Широта').value, '58.01');

  view.unmount();
  cleanup();
});
