import React from 'react';
import { Button } from '@maxhub/max-ui';

const dataModeLabels = {
  live: 'Данные из действующего подключения',
  prepared: 'Заранее полученные данные',
  synthetic: 'Модельные данные',
};
const archetypeLabels = {
  urban_avantgarde: 'Современный город',
  history_heritage: 'История и культура',
  action_social: 'Активность и общество',
};

function formatTimestamp(value) {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):\d{2}([+-]\d{2}:\d{2}|Z)$/.exec(value || '');
  if (!match) return value || 'Не указано';
  return `${match[3]}.${match[2]}.${match[1]}, ${match[4]}:${match[5]} UTC${match[6] === 'Z' ? '+00:00' : match[6]}`;
}

function formatMoney(money) {
  if (!money || !/^(0|[1-9][0-9]*)$/.test(money.amount_minor) || !/^[A-Z]{3}$/.test(money.currency)) {
    return 'Не указаны';
  }
  try {
    const digits = new Intl.NumberFormat('ru-RU', { style: 'currency', currency: money.currency })
      .resolvedOptions().maximumFractionDigits;
    const scale = 10n ** BigInt(digits);
    const amount = BigInt(money.amount_minor);
    const whole = new Intl.NumberFormat('ru-RU').format(amount / scale);
    const fraction = digits ? `,${String(amount % scale).padStart(digits, '0')}` : '';
    return `${whole}${fraction} ${money.currency}`;
  } catch {
    return 'Не указаны';
  }
}

function Details({ items, title }) {
  if (!items?.length) return null;
  return (
    <section className="result-details">
      <h3>{title}</h3>
      <ul>{items.map((item, index) => <li key={`${item.code}-${index}`}>{item.message}</li>)}</ul>
    </section>
  );
}

function RouteCard({ route, number }) {
  const plan = route.plan;
  if (!plan || !plan.cost) {
    return <li className="route-card">Вариант {number}: данные маршрута неполны.</li>;
  }
  return (
    <li className="route-card">
      <h3>Вариант {number}</h3>
      <dl>
        <div><dt>Направление</dt><dd>{archetypeLabels[plan.archetype_id] || plan.archetype_id}</dd></div>
        <div><dt>Время</dt><dd>{formatTimestamp(plan.start_at)} — {formatTimestamp(plan.end_at)}</dd></div>
        <div><dt>Личные расходы, известная часть</dt><dd>{formatMoney(plan.cost.known_personal)}</dd></div>
        <div><dt>Статус бюджета</dt><dd>{plan.cost.budget_conclusion === 'satisfied' ? 'Известные условия выполнены' : plan.cost.budget_conclusion === 'violated' ? 'Превышен' : 'Не подтверждён'}</dd></div>
        <div><dt>Черновик</dt><dd>{route.route_id}, версия {route.revision}</dd></div>
      </dl>
      <Details title="Неизвестные расходы" items={plan.cost.unknown_components} />
    </li>
  );
}

export default function ResultsView({ state, onRetry }) {
  if (state.kind === 'loading') {
    return <section className="results-card" aria-live="polite"><h2>Рассчитываем маршрут…</h2><p>Подождите ответа сервиса.</p></section>;
  }

  if (state.kind === 'error') {
    const { error } = state;
    const title = error.httpStatus === 401 ? 'Требуется повторный вход'
      : error.httpStatus === 409 ? 'Команда конфликтует с текущим состоянием'
        : error.httpStatus === 429 ? 'Слишком много запросов'
          : error.httpStatus === 503 ? 'Сервис временно недоступен' : 'Не удалось получить ответ';
    return (
      <section className="results-card" role="alert">
        <h2>{title}</h2>
        <p>{error.message}</p>
        {error.code && <p className="result-meta">Код: {error.code}</p>}
        {error.retryable && onRetry && <Button type="button" mode="secondary" onClick={onRetry}>Повторить</Button>}
      </section>
    );
  }

  const { result } = state;
  const refused = result.status === 'NO_FEASIBLE_ROUTE' || result.status === 'CONFLICT';
  const title = result.status === 'NO_FEASIBLE_ROUTE' ? 'Подходящий маршрут не найден'
    : result.status === 'CONFLICT' ? 'Обязательные условия конфликтуют'
      : result.status === 'PARTIAL' ? 'Найдена часть вариантов' : 'Варианты маршрута';

  return (
    <section className="results-card" aria-live="polite">
      <h2>{title}</h2>
      <p className="result-meta">{dataModeLabels[result.data_mode] || 'Режим данных не указан'}. {result.data_as_of ? `Актуальность: ${result.data_as_of}.` : 'Время актуальности не указано.'}</p>
      <Details title="Предупреждения" items={result.warnings} />
      <Details title="Причины конфликта" items={result.conflicts} />
      {refused ? (
        !result.warnings?.length && !result.conflicts?.length && <p>Сервис не передал подробное объяснение. Измените условия и повторите попытку.</p>
      ) : result.routes?.length ? (
        <ol className="route-list">{result.routes.slice(0, 3).map((route, index) => <RouteCard key={route.route_id} route={route} number={index + 1} />)}</ol>
      ) : (
        <p>В ответе нет вариантов маршрута.</p>
      )}
      {result.status === 'PARTIAL' && <p className="result-note">Проверьте предупреждения и неизвестные условия до выбора маршрута.</p>}
    </section>
  );
}
