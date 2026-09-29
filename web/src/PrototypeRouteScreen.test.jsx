import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createElement } from 'react';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/?demo=balance' });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.MutationObserver = dom.window.MutationObserver;

const { cleanup, fireEvent, render, screen } = await import('@testing-library/react');
const { default: PrototypeRouteScreen } = await import('./PrototypeRouteScreen.jsx');

test('opens a route with distinct variants, map, timeline and truthful stop detail', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    assert.ok(screen.getByRole('heading', { name: 'Вариант дня' }));
    assert.ok(screen.getByRole('heading', { name: 'По пути' }));
    assert.ok(screen.getByText(/Демонстрационный маршрут/));
    fireEvent.click(screen.getByRole('button', { name: '13:25 Музей современного искусства PERMM' }));
    assert.ok(screen.getByRole('dialog', { name: 'Музей современного искусства PERMM' }));
    assert.ok(screen.getByText(/фактическая цена не подтверждена/));
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
    fireEvent.click(screen.getByRole('button', { name: /История и культура/ }));
    assert.ok(screen.getByRole('button', { name: '13:30 Пермская художественная галерея' }));
  } finally { cleanup(); }
});

test('shows a delay proposal and applies it only after explicit confirmation', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Опаздываю' }));
    assert.ok(screen.getByRole('dialog', { name: 'Если вы опаздываете' }));
    fireEvent.click(screen.getByRole('button', { name: 'Посмотреть изменения' }));
    assert.ok(screen.getByRole('dialog', { name: 'Предложение изменений' }));
    assert.ok(screen.getByText('Финиш 16:50'));
    assert.ok(screen.getByText('Финиш 16:20'));
    fireEvent.click(screen.getByRole('button', { name: 'Применить к демо' }));
    assert.ok(screen.getByText(/версия 2/i));
    assert.ok(screen.getByText(/Модельное изменение применено/));
  } finally { cleanup(); }
});

test('keeps manual endpoints pending until the user accepts them', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Настройки маршрута' }));
    fireEvent.click(screen.getByText('Карта недоступна? Ввести адрес'));
    fireEvent.change(screen.getByLabelText('Место старта'), { target: { value: 'Пермь II' } });
    fireEvent.change(screen.getByLabelText('Место финиша'), { target: { value: 'Эспланада' } });
    fireEvent.click(screen.getByRole('button', { name: 'Применить адреса' }));
    assert.ok(screen.getByRole('dialog', { name: 'Предложение изменений' }));
    fireEvent.click(screen.getByRole('button', { name: 'Применить к демо' }));
    assert.ok(screen.getByText(/Пермь II.*Эспланада/));
  } finally { cleanup(); }
});

test('adds a free lunch and marks a second lunch for rescheduling', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Обед' }));
    fireEvent.click(screen.getByRole('button', { name: 'Оставить свободное время без кафе' }));
    assert.ok(screen.getAllByText('Свободное время на обед').length > 0);
    fireEvent.click(screen.getByRole('button', { name: 'Обед' }));
    fireEvent.change(screen.getByLabelText('Время на обед'), { target: { value: '60' } });
    fireEvent.click(screen.getByRole('button', { name: 'Оставить свободное время без кафе' }));
    assert.ok(screen.getAllByText(/Время требует пересчёта/).length > 0);
  } finally { cleanup(); }
});

test('a pinned stop blocks replacement by another sample route in the same theme', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: '13:25 Музей современного искусства PERMM' }));
    fireEvent.click(screen.getByRole('button', { name: 'Закрепить точку' }));
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
    fireEvent.click(screen.getByRole('button', { name: 'Перестроить' }));
    assert.ok(screen.getByText(/Конфликт:/));
    assert.equal(screen.getByRole('button', { name: 'Применить к демо' }).disabled, true);
  } finally { cleanup(); }
});
