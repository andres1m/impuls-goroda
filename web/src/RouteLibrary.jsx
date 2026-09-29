import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import { cities } from './input.js';
import { loadOwnerRoute, loadRoutePage } from './route.js';
import { archetypeTitles } from './routeProjection.js';

const archetypeDescriptions = {
  urban_avantgarde: 'Современные пространства, арт-кластеры и городской ритм',
  history_heritage: 'Знаковые памятники, музеи и культурное наследие',
  action_social: 'Активный темп, парки, события и полезные активности',
};

function cityLabel(code) {
  if (!code) return 'Город не указан';
  const normalized = String(code).trim().toLowerCase();
  return cities[normalized]?.name || code;
}

function formatDateBadge(startAt, timezone) {
  try {
    return new Intl.DateTimeFormat('ru-RU', {
      timeZone: timezone,
      day: 'numeric',
      month: 'short',
      weekday: 'short',
    }).format(new Date(startAt));
  } catch {
    return '';
  }
}

function formatTimeWindow(startAt, endAt, timezone) {
  try {
    const fmt = new Intl.DateTimeFormat('ru-RU', {
      timeZone: timezone,
      hour: '2-digit',
      minute: '2-digit',
    });
    return `${fmt.format(new Date(startAt))} – ${fmt.format(new Date(endAt))}`;
  } catch {
    return '';
  }
}

function formatDuration(startAt, endAt) {
  const ms = Date.parse(endAt) - Date.parse(startAt);
  if (!Number.isFinite(ms) || ms <= 0) return '';
  const totalMinutes = Math.round(ms / 60000);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours > 0 && minutes > 0) return `${hours} ч ${minutes} мин`;
  if (hours > 0) return `${hours} ч`;
  return `${minutes} мин`;
}

function formatUpdatedAt(updatedAt, timezone) {
  try {
    return new Intl.DateTimeFormat('ru-RU', {
      timeZone: timezone,
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(updatedAt));
  } catch {
    return '';
  }
}

export default function RouteLibrary({ apiBaseUrl, accessToken, mapApiKey, onBack, onAuthRequired, onCreate }) {
  const [lifecycle, setLifecycle] = useState('saved');
  const [attempt, setAttempt] = useState(0);
  const [page, setPage] = useState({ state: 'loading', routes: [], nextCursor: null });
  const [owner, setOwner] = useState({ state: 'idle' });
  const controller = useRef(null);

  useEffect(() => {
    const request = new AbortController();
    controller.current = request;
    setPage({ state: 'loading', routes: [], nextCursor: null });
    loadRoutePage(apiBaseUrl, accessToken, lifecycle, null, request.signal)
      .then((value) => { if (!request.signal.aborted) setPage({ state: 'ready', ...value }); })
      .catch((error) => {
        if (request.signal.aborted) return;
        if (error.status === 401) onAuthRequired();
        else setPage({ state: 'error', routes: [], nextCursor: null });
      });
    return () => { request.abort(); controller.current?.abort(); };
  }, [apiBaseUrl, accessToken, lifecycle, attempt, onAuthRequired]);

  async function more() {
    if (page.state !== 'ready' || !page.nextCursor) return;
    const request = new AbortController();
    controller.current = request;
    setPage((value) => ({ ...value, state: 'more' }));
    try {
      const value = await loadRoutePage(apiBaseUrl, accessToken, lifecycle, page.nextCursor, request.signal);
      if (request.signal.aborted) return;
      setPage((previous) => {
        const ids = new Set(previous.routes.map((item) => item.route_id));
        return { state: 'ready', routes: [...previous.routes, ...value.routes.filter((item) => !ids.has(item.route_id))], nextCursor: value.nextCursor };
      });
    } catch (error) {
      if (request.signal.aborted) return;
      if (error.status === 401) onAuthRequired();
      else setPage((value) => ({ ...value, state: 'ready', moreError: true }));
    }
  }

  async function open(id) {
    if (page.state !== 'ready' || owner.state === 'loading') return;
    const request = new AbortController();
    controller.current = request;
    setOwner({ state: 'loading', id });
    try {
      const value = await loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, request.signal);
      if (!request.signal.aborted) setOwner({ state: 'ready', value });
    } catch (error) {
      if (request.signal.aborted) return;
      if (error.status === 401) onAuthRequired();
      else setOwner({ state: 'error', id, missing: error.status === 404 });
    }
  }

  if (owner.state === 'ready') return <OwnerRouteScreen key={owner.value.route_id} route={owner.value} apiBaseUrl={apiBaseUrl} accessToken={accessToken} mapApiKey={mapApiKey} backLabel="Мои маршруты" onBack={() => { setOwner({ state: 'idle' }); setAttempt((value) => value + 1); }} />;
  const busy = ['loading', 'more'].includes(page.state) || owner.state === 'loading';
  return <main className="entry-page route-library">
    <header className="route-library-topbar">
      <Brand className="entry-brand" />
      <div className="route-library-topbar-actions">
        {page.state === 'ready' && (
          <button type="button" className="route-library-refresh-btn" disabled={busy} onClick={() => { setOwner({ state: 'idle' }); setAttempt((value) => value + 1); }}>
            <span aria-hidden="true">↻</span> Обновить список
          </button>
        )}
        <button type="button" className="route-library-nav-btn" onClick={onBack}>← Главное меню</button>
      </div>
    </header>

    <div className="route-library-hero">
      <div className="route-library-hero-copy">
        <h1>Мои маршруты</h1>
        <p className="route-library-subtitle">Ваши готовые планы прогулок и черновики сценариев</p>
      </div>
      {onCreate && (
        <button type="button" className="route-library-create-btn" onClick={onCreate}>
          + Создать маршрут
        </button>
      )}
    </div>

    <div className="route-library-filters" role="group" aria-label="Тип маршрутов">
      {[['saved', 'Сохранённые'], ['draft', 'Черновики']].map(([value, label]) => (
        <button
          key={value}
          type="button"
          className="route-library-tab"
          disabled={busy}
          aria-pressed={lifecycle === value}
          onClick={() => { setOwner({ state: 'idle' }); setLifecycle(value); }}
        >
          <span>{label}</span>
          {lifecycle === value && page.state === 'ready' && (
            <span className="route-library-tab-count">{page.routes.length}</span>
          )}
        </button>
      ))}
    </div>

    <section className="route-library-body" aria-live="polite" aria-busy={busy}>
      {page.state === 'loading' && (
        <div className="route-library-state-card">
          <strong>Загружаем маршруты…</strong>
          <p>Проверяем сохранённые планы и черновики прогулок.</p>
        </div>
      )}
      {page.state === 'error' && (
        <div className="route-library-state-card is-error">
          <strong>Не удалось загрузить список.</strong>
          <p>Проверьте соединение и попробуйте обновить страницу.</p>
          <button type="button" className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button>
        </div>
      )}
      {page.state === 'ready' && !page.routes.length && (
        <div className="route-library-state-card is-empty">
          <strong>{lifecycle === 'saved' ? 'Сохранённых маршрутов пока нет.' : 'Черновиков маршрутов пока нет.'}</strong>
          <p>{lifecycle === 'saved' ? 'Сохраняйте понравившиеся варианты после расчёта, чтобы быстро открывать их в дороге.' : 'Начните новый сценарий дня — черновик появится здесь автоматически.'}</p>
        </div>
      )}
      {owner.state === 'loading' && <p className="route-library-inline-status">Открываем маршрут…</p>}
      {owner.state === 'error' && <p className="route-library-inline-alert" role="alert">{owner.missing ? 'Маршрут больше недоступен. Обновите список.' : 'Не удалось открыть маршрут. Нажмите на него ещё раз.'}</p>}
      <ul className="route-library-list">
        {page.routes.map((item) => {
          const title = archetypeTitles[item.archetype_id] || 'Маршрут';
          const subtitle = archetypeDescriptions[item.archetype_id] || 'Персональный городской маршрут';
          const city = cityLabel(item.city);
          const dateBadge = formatDateBadge(item.start_at, item.timezone);
          const windowBadge = formatTimeWindow(item.start_at, item.end_at, item.timezone);
          const duration = formatDuration(item.start_at, item.end_at);
          const updated = formatUpdatedAt(item.updated_at, item.timezone);
          const isPartial = item.result === 'PARTIAL';
          const isOpening = owner.state === 'loading' && owner.id === item.route_id;
          return (
            <li key={item.route_id}>
              <button
                type="button"
                className="route-library-card"
                data-archetype={item.archetype_id}
                disabled={busy}
                onClick={() => open(item.route_id)}
              >
                <div className="route-library-card-top">
                  <span className="route-library-archetype-tag">
                    {item.lifecycle === 'saved' ? 'Сохранённый маршрут' : 'Черновик'} · v{item.revision}
                  </span>
                  <span className={`route-library-status ${isPartial ? 'is-partial' : 'is-ready'}`}>
                    <span className="route-library-status-dot" aria-hidden="true" />
                    {isPartial ? 'Есть ограничения' : 'Готов к прогулке'}
                  </span>
                </div>
                <div className="route-library-card-body">
                  <strong className="route-library-card-title">{title}</strong>
                  <span className="route-library-card-desc">{subtitle}</span>
                </div>
                <div className="route-library-card-meta">
                  <span className="route-library-chip">{city}</span>
                  {dateBadge && <span className="route-library-chip">{dateBadge}</span>}
                  {windowBadge && <span className="route-library-chip">{windowBadge}</span>}
                  {duration && <span className="route-library-chip">{duration}</span>}
                </div>
                <div className="route-library-card-footer">
                  <span className="route-library-card-updated">
                    {updated ? `Обновлён ${updated}` : ''}
                  </span>
                  <span className="route-library-card-cta">
                    {isOpening ? 'Открываем…' : 'Открыть маршрут'} <span aria-hidden="true">→</span>
                  </span>
                </div>
              </button>
            </li>
          );
        })}
      </ul>
      {page.moreError && <p className="route-library-inline-alert" role="alert">Не удалось загрузить следующие маршруты.</p>}
      {page.nextCursor && <button type="button" className="scenario-option" disabled={busy} onClick={more}>{page.state === 'more' ? 'Загружаем…' : page.moreError ? 'Повторить загрузку' : 'Показать ещё'}</button>}
    </section>
  </main>;
}
