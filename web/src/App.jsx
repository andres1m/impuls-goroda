import React, { useEffect, useState } from 'react';
import { Button } from '@maxhub/max-ui';
import { exchangeMaxInitData } from './auth.js';
import { loadRuntimeConfig } from './config.js';
import { loadSelectedRoute, RouteRequestError } from './route.js';
import RouteScreen from './RouteScreen.jsx';
import PrototypeRouteScreen from './PrototypeRouteScreen.jsx';

export default function App() {
  const [config, setConfig] = useState({ state: 'loading' });
  const [session, setSession] = useState({ state: 'idle' });
  const [route, setRoute] = useState({ state: 'idle' });
  const [configAttempt, setConfigAttempt] = useState(0);
  const [authAttempt, setAuthAttempt] = useState(0);
  const [routeAttempt, setRouteAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setConfig({ state: 'loading' });
    loadRuntimeConfig(new URL('config.json', document.baseURI), fetch, controller.signal)
      .then((value) => { if (!controller.signal.aborted) setConfig({ state: 'ready', value }); })
      .catch(() => { if (!controller.signal.aborted) setConfig({ state: 'error' }); });
    return () => controller.abort();
  }, [configAttempt]);

  useEffect(() => {
    if (config.state !== 'ready' || config.value.prototypeMode) {
      setSession({ state: 'idle' });
      return;
    }
    const initData = window.WebApp?.initData;
    if (typeof initData !== 'string' || initData.length === 0) {
      setSession({ state: 'unavailable' });
      return;
    }
    const controller = new AbortController();
    setSession({ state: 'loading' });
    exchangeMaxInitData(config.value.apiBaseUrl, initData, fetch, controller.signal)
      .then((value) => { if (!controller.signal.aborted) setSession({ state: 'ready', value }); })
      .catch(() => { if (!controller.signal.aborted) setSession({ state: 'error' }); });
    return () => controller.abort();
  }, [config, authAttempt]);

  useEffect(() => {
    if (config.state !== 'ready' || config.value.prototypeMode || session.state !== 'ready') {
      setRoute({ state: 'idle' });
      return;
    }
    const controller = new AbortController();
    setRoute({ state: 'loading' });
    loadSelectedRoute(config.value.apiBaseUrl, session.value.accessToken, fetch, controller.signal)
      .then((value) => { if (!controller.signal.aborted) setRoute(value ? { state: 'ready', value } : { state: 'empty' }); })
      .catch((error) => {
        if (controller.signal.aborted) return;
        if (error instanceof RouteRequestError && error.status === 401) {
          setSession({ state: 'error' });
          return;
        }
        setRoute({ state: 'error', error });
      });
    return () => controller.abort();
  }, [config, session, routeAttempt]);

  if (config.state === 'ready' && config.value.prototypeMode) return <PrototypeRouteScreen mapApiKey={config.value.twoGisApiKey} />;
  if (route.state === 'ready') return <RouteScreen route={route.value} />;

  let title = 'Открываем маршрут';
  let message = 'Подключаемся к MAX и загружаем ваш маршрут.';
  let retry = null;
  if (config.state === 'error') {
    title = 'Нет подключения к сервису';
    message = 'Не удалось получить адрес API. Проверьте соединение и повторите попытку.';
    retry = () => setConfigAttempt((attempt) => attempt + 1);
  } else if (session.state === 'unavailable') {
    title = 'Откройте приложение в MAX';
    message = 'Для просмотра личного маршрута нужен вход через MAX.';
    retry = () => setAuthAttempt((attempt) => attempt + 1);
  } else if (session.state === 'error') {
    title = 'Не удалось войти';
    message = 'Повторите вход. Если данные запуска устарели, закройте Mini App и откройте её снова в MAX.';
    retry = () => setAuthAttempt((attempt) => attempt + 1);
  } else if (route.state === 'empty') {
    title = 'Маршрута пока нет';
    message = 'Создайте сценарий в боте MAX, затем откройте Mini App снова.';
    retry = () => setRouteAttempt((attempt) => attempt + 1);
  } else if (route.state === 'error') {
    title = 'Не удалось загрузить маршрут';
    message = route.error instanceof RouteRequestError && route.error.status === 404
      ? 'Выбранный маршрут недоступен. Вернитесь в бот и откройте актуальный маршрут.'
      : 'Проверьте соединение и попробуйте снова.';
    retry = () => setRouteAttempt((attempt) => attempt + 1);
  }

  return (
    <main className="entry-page">
      <header className="entry-brand">ИМПУЛЬС ГОРОДА</header>
      <section className="entry-state" aria-live="polite">
        <span className="entry-rule" aria-hidden="true" />
        <h1>{title}</h1>
        <p>{message}</p>
        {retry && <Button onClick={retry}>Повторить</Button>}
      </section>
    </main>
  );
}
