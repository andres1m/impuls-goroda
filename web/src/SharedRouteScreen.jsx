import React, { useEffect, useMemo, useState } from 'react';
import Brand from './Brand.jsx';
import RouteScreen from './RouteScreen.jsx';
import { loadSharedRoute } from './sharing.js';

export default function SharedRouteScreen({ apiBaseUrl, token, mapApiKey }) {
  const [snapshot, setSnapshot] = useState({ state: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setSnapshot({ state: 'loading' });
    loadSharedRoute(apiBaseUrl, token, fetch, controller.signal)
      .then((value) => { if (!controller.signal.aborted) setSnapshot({ state: 'ready', value }); })
      .catch((error) => { if (!controller.signal.aborted) setSnapshot({ state: error.status === 404 ? 'unavailable' : 'error' }); });
    return () => controller.abort();
  }, [apiBaseUrl, token, attempt]);

  const display = useMemo(() => {
    if (snapshot.state !== 'ready') return null;
    const { revision, issues, updated_at, city, ...plan } = snapshot.value;
    return { revision, issues, updated_at, city, plan };
  }, [snapshot]);

  if (display) return <>
    <RouteScreen route={display} mapApiKey={mapApiKey} shared />
    <section className="owner-route-actions" aria-label="Обновление общего маршрута">
      <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Обновить маршрут</button>
    </section>
  </>;

  const title = snapshot.state === 'loading' ? 'Открываем маршрут' : snapshot.state === 'unavailable' ? 'Ссылка недоступна' : 'Не удалось загрузить маршрут';
  const message = snapshot.state === 'loading' ? 'Загружаем общий маршрут.' : snapshot.state === 'unavailable' ? 'Попросите автора отправить новую ссылку.' : 'Проверьте соединение и попробуйте снова.';
  return <main className="entry-page">
    <header><Brand className="entry-brand" /></header>
    <section className="entry-state" aria-live="polite">
      <span className="entry-rule" aria-hidden="true" />
      <h1>{title}</h1><p>{message}</p>
      {snapshot.state !== 'loading' && <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button>}
    </section>
  </main>;
}
