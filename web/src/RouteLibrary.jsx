import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import { loadOwnerRoute, loadRoutePage } from './route.js';
import { archetypeTitles } from './routeProjection.js';

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
    <header><Brand className="entry-brand" /></header>
    <button className="scenario-option" onClick={onBack}>Главное меню</button>
    <h1>Мои маршруты</h1>
    {onCreate && <button className="scenario-option scenario-primary" onClick={onCreate}>Создать маршрут</button>}
    <div className="route-library-filters" aria-label="Тип маршрутов">
      {[['saved', 'Сохранённые'], ['draft', 'Черновики']].map(([value, label]) => <button key={value} className="scenario-option" disabled={busy} aria-pressed={lifecycle === value} onClick={() => { setOwner({ state: 'idle' }); setLifecycle(value); }}>{label}</button>)}
    </div>
    <section aria-live="polite" aria-busy={busy}>
      {page.state === 'loading' && <p>Загружаем маршруты…</p>}
      {page.state === 'error' && <><p>Не удалось загрузить список.</p><button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button></>}
      {page.state === 'ready' && !page.routes.length && <p>{lifecycle === 'saved' ? 'Сохранённых маршрутов пока нет.' : 'Черновиков маршрутов пока нет.'}</p>}
      {owner.state === 'loading' && <p>Открываем маршрут…</p>}
      {owner.state === 'error' && <p role="alert">{owner.missing ? 'Маршрут больше недоступен. Обновите список.' : 'Не удалось открыть маршрут. Нажмите на него ещё раз.'}</p>}
      <ul className="route-library-list">{page.routes.map((item) => <li key={item.route_id}>
        <button className="route-library-card" disabled={busy} onClick={() => open(item.route_id)}>
          <strong>{archetypeTitles[item.archetype_id] || 'Маршрут'}</strong>
          <span>{item.city} · {new Intl.DateTimeFormat('ru-RU', { timeZone: item.timezone, day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(item.start_at))}</span>
          <span>{item.result === 'PARTIAL' ? 'Есть ограничения' : 'Открыть маршрут'}</span>
        </button>
      </li>)}</ul>
      {page.moreError && <p role="alert">Не удалось загрузить следующие маршруты.</p>}
      {page.nextCursor && <button className="scenario-option" disabled={busy} onClick={more}>{page.state === 'more' ? 'Загружаем…' : page.moreError ? 'Повторить загрузку' : 'Показать ещё'}</button>}
      {page.state === 'ready' && <button className="scenario-option" disabled={busy} onClick={() => { setOwner({ state: 'idle' }); setAttempt((value) => value + 1); }}>Обновить список</button>}
    </section>
  </main>;
}
