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
    assert.ok(screen.getByRole('heading', { name: 'Ваш день в городе' }));
    assert.ok(screen.getByLabelText('Карта маршрута 2ГИС'));
    assert.ok(screen.getByRole('heading', { name: 'Расписание' }));
    assert.ok(screen.getByText('ДЕМО · ДАННЫЕ ВЫМЫШЛЕНЫ'));
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
    assert.ok(screen.getByText(/ВЕРСИЯ 2/));
    assert.ok(screen.getByText(/Модельное изменение применено/));
  } finally { cleanup(); }
});

test('keeps manual endpoints pending until the user accepts them', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: /Условия/ }));
    fireEvent.change(screen.getByLabelText('Место старта'), { target: { value: 'Пермь II' } });
    fireEvent.change(screen.getByLabelText('Место финиша, необязательно'), { target: { value: 'Эспланада' } });
    fireEvent.click(screen.getByRole('button', { name: 'Предложить старт и финиш' }));
    assert.ok(screen.getByRole('dialog', { name: 'Предложение изменений' }));
    fireEvent.click(screen.getByRole('button', { name: 'Применить к демо' }));
    assert.ok(screen.getByText(/Старт: Пермь II/));
    assert.ok(screen.getByText(/Финиш: Эспланада/));
  } finally { cleanup(); }
});

test('adds a free lunch only after confirmation and refuses a late 60-minute proposal', () => {
  window.localStorage.clear();
  render(createElement(PrototypeRouteScreen));
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Обед' }));
    fireEvent.click(screen.getByRole('button', { name: 'Посмотреть изменения' }));
    assert.ok(screen.getByText('Финиш 16:55'));
    fireEvent.click(screen.getByRole('button', { name: 'Применить к демо' }));
    assert.ok(screen.getByText('Свободное время на обед'));
    fireEvent.click(screen.getByRole('button', { name: 'Обед' }));
    fireEvent.change(screen.getByLabelText('Время на обед'), { target: { value: '60' } });
    fireEvent.click(screen.getByRole('button', { name: 'Посмотреть изменения' }));
    assert.ok(screen.getByText('Финиш 17:10'));
    assert.equal(screen.getByRole('button', { name: 'Применить к демо' }).disabled, true);
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
