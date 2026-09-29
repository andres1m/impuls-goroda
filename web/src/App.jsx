import React, { useCallback, useEffect, useState } from 'react';
import { Button } from '@maxhub/max-ui';
import { exchangeMaxInitData } from './auth.js';
import { loadRuntimeConfig } from './config.js';
import { loadOwnerRoute, loadSelectedRoute, RouteRequestError } from './route.js';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import PrototypeRouteScreen from './PrototypeRouteScreen.jsx';
import Brand from './Brand.jsx';
import { readLaunchContext } from './launch.js';
import { loadScenario } from './scenario.js';
import { GatewayError } from './optimize.js';
import ScenarioEntry from './ScenarioEntry.jsx';
import SharedRouteScreen from './SharedRouteScreen.jsx';
import RouteLibrary from './RouteLibrary.jsx';
import ScenarioCreate from './ScenarioCreate.jsx';

export default function App() {
  const [config, setConfig] = useState({ state: 'loading' });
  const [session, setSession] = useState({ state: 'idle' });
  const [route, setRoute] = useState({ state: 'idle' });
  const [launch, setLaunch] = useState({ state: 'idle' });
  const [configAttempt, setConfigAttempt] = useState(0);
  const [authAttempt, setAuthAttempt] = useState(0);
  const [routeAttempt, setRouteAttempt] = useState(0);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const requireAuth = useCallback(() => { setLibraryOpen(false); setSession({ state: 'error' }); }, []);

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
      setLaunch({ state: 'idle' });
      return;
    }
    let active = true;
    setLaunch({ state: 'loading' });
    readLaunchContext()
      .then((value) => { if (active) setLaunch({ state: 'ready', value }); })
      .catch(() => { if (active) setLaunch({ state: 'error' }); });
    return () => { active = false; };
  }, [config, authAttempt]);

  useEffect(() => {
    if (config.state !== 'ready' || config.value.prototypeMode || launch.state !== 'ready' || ['shared', 'demo'].includes(launch.value.kind)) {
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
  }, [config, launch, authAttempt]);

  useEffect(() => {
    if (config.state !== 'ready' || config.value.prototypeMode || launch.state !== 'ready' || launch.value.kind === 'shared') {
      setRoute({ state: 'idle' });
      return;
    }
    if (launch.value.kind === 'demo') { setRoute({ state: 'demo' }); return; }
    if (session.state !== 'ready') { setRoute({ state: 'idle' }); return; }
    const controller = new AbortController();
    setRoute({ state: 'loading' });
    async function openEntry() {
      if (launch.value.kind === 'owner') {
        const value = await loadOwnerRoute(config.value.apiBaseUrl, session.value.accessToken, launch.value.routeID, fetch, controller.signal);
        return { state: 'ready', value };
      }
      if (launch.value.kind === 'scenario') {
        const value = await loadScenario(config.value.apiBaseUrl, session.value.accessToken, launch.value.scenarioID, fetch, controller.signal);
        return { state: 'scenario', value };
      }
      const selected = await loadSelectedRoute(config.value.apiBaseUrl, session.value.accessToken, fetch, controller.signal);
      if (selected) return { state: 'ready', value: selected };
      const scenario = await loadScenario(config.value.apiBaseUrl, session.value.accessToken, null, fetch, controller.signal);
      return scenario ? { state: 'scenario', value: scenario } : { state: 'empty' };
    }
    openEntry()
      .then((value) => { if (!controller.signal.aborted && value) setRoute(value); })
      .catch((error) => {
        if (controller.signal.aborted) return;
        if ((error instanceof RouteRequestError && error.status === 401) || (error instanceof GatewayError && error.httpStatus === 401)) {
          setSession({ state: 'error' });
          return;
        }
        setRoute({ state: 'error', error });
      });
    return () => controller.abort();
  }, [config, launch, session, routeAttempt]);

  if (config.state === 'ready' && config.value.prototypeMode) return <PrototypeRouteScreen mapApiKey={config.value.twoGisApiKey} />;
  const availability = null;
  if (config.state === 'ready' && launch.state === 'ready' && launch.value.kind === 'shared') return <>{availability}<SharedRouteScreen key={launch.value.shareToken} token={launch.value.shareToken} apiBaseUrl={config.value.apiBaseUrl} mapApiKey={config.value.twoGisApiKey} /></>;
  if (config.state === 'ready' && session.state === 'ready' && libraryOpen) return <>{availability}<RouteLibrary apiBaseUrl={config.value.apiBaseUrl} accessToken={session.value.accessToken} mapApiKey={config.value.twoGisApiKey} onCreate={() => { setLibraryOpen(false); setRoute({ state: 'empty' }); }} onAuthRequired={requireAuth} onBack={() => { setLibraryOpen(false); setRoute({ state: 'empty' }); }} /></>;
  if (config.state === 'ready' && session.state === 'ready' && route.state === 'empty') return <>{availability}<ScenarioCreate apiBaseUrl={config.value.apiBaseUrl} accessToken={session.value.accessToken} onCreated={(value) => setRoute({ state: 'scenario', value })} onLibrary={() => setLibraryOpen(true)} onAuthRequired={requireAuth} /></>;
  if (config.state === 'ready' && session.state === 'ready' && route.state === 'ready') return <>{availability}<OwnerRouteScreen key={route.value.route_id} route={route.value} apiBaseUrl={config.value.apiBaseUrl} accessToken={session.value.accessToken} mapApiKey={config.value.twoGisApiKey} backLabel="Мои маршруты" onBack={() => setLibraryOpen(true)} /></>;
  if (config.state === 'ready' && session.state === 'ready' && route.state === 'scenario') return <>{availability}<ScenarioEntry key={route.value.scenario_id} scenario={route.value} apiBaseUrl={config.value.apiBaseUrl} accessToken={session.value.accessToken} mapApiKey={config.value.twoGisApiKey} onLibrary={() => setLibraryOpen(true)} onReload={() => setRouteAttempt((attempt) => attempt + 1)} /></>;

  let title = 'Открываем маршрут';
  let message = 'Подключаемся к MAX и загружаем ваш маршрут.';
  let retry = null;
  if (config.state === 'error') {
    title = 'Нет подключения к сервису';
    message = 'Не удалось получить адрес API. Проверьте соединение и повторите попытку.';
    retry = () => setConfigAttempt((attempt) => attempt + 1);
  } else if (launch.state === 'error') {
    title = 'Не удалось открыть ссылку';
    message = 'Вернитесь в бот и откройте Mini App снова.';
    retry = () => setAuthAttempt((attempt) => attempt + 1);
  } else if (session.state === 'unavailable') {
    title = 'Откройте приложение в MAX';
    message = 'Для просмотра личного маршрута нужен вход через MAX.';
    retry = () => setAuthAttempt((attempt) => attempt + 1);
  } else if (session.state === 'error') {
    title = 'Не удалось войти';
    message = 'Повторите вход. Если данные запуска устарели, закройте Mini App и откройте её снова в MAX.';
    retry = () => setAuthAttempt((attempt) => attempt + 1);
  } else if (route.state === 'demo') {
    title = 'Демонстрационная ссылка';
    message = 'Для нового маршрута выберите сценарий в боте и откройте его новую ссылку.';
  } else if (route.state === 'error') {
    title = 'Не удалось загрузить маршрут';
    message = route.error instanceof RouteRequestError && route.error.status === 404
      ? 'Выбранный маршрут недоступен. Вернитесь в бот и откройте актуальный маршрут.'
      : 'Проверьте соединение и попробуйте снова.';
    retry = () => setRouteAttempt((attempt) => attempt + 1);
  }

  return (
    <>{availability}
    <main className="entry-page">
      <header><Brand className="entry-brand" /></header>
      <section className="entry-state" aria-live="polite">
        <span className="entry-rule" aria-hidden="true" />
        <h1>{title}</h1>
        <p>{message}</p>
        {retry && <Button onClick={retry}>Повторить</Button>}
        {session.state === 'ready' && ['empty', 'error'].includes(route.state) && <button className="scenario-option" onClick={() => setLibraryOpen(true)}>Мои маршруты</button>}
      </section>
    </main></>
  );
}
