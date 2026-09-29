import React, { useEffect, useMemo, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import RouteScreen from './RouteScreen.jsx';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';
import { exchangeMaxInitData } from './auth.js';
import { loadOwnerRoute, RouteRequestError } from './route.js';
import { userMessage } from './messages.js';
import { createCopyAttempt, loadSharedRoute, sendCopyCommand } from './sharing.js';

export default function SharedRouteScreen({ apiBaseUrl, token, mapApiKey }) {
  const [snapshot, setSnapshot] = useState({ state: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [copyOpen, setCopyOpen] = useState(false);
  const [latitude, setLatitude] = useState('');
  const [longitude, setLongitude] = useState('');
  const [selectedUnknowns, setSelectedUnknowns] = useState([]);
  const [pickOnMap, setPickOnMap] = useState(false);
  const [copyBusy, setCopyBusy] = useState(false);
  const [copyMessage, setCopyMessage] = useState('');
  const [copyConflicts, setCopyConflicts] = useState([]);
  const [copied, setCopied] = useState(null);
  const copyAttempt = useRef(null);
  const copyRequest = useRef(null);
  useEffect(() => () => copyRequest.current?.abort(), []);
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

  const unknowns = useMemo(() => {
    if (snapshot.state !== 'ready') return [];
    const items = [...(snapshot.value.cost.unknown_components || [])];
    for (const step of snapshot.value.steps) items.push(...(step.cost?.unknown_components || []));
    for (const leg of snapshot.value.legs) items.push(...(leg.cost?.unknown_components || []));
    return [...new Map(items.filter((item) => item?.code).map((item) => [item.code, item])).values()];
  }, [snapshot]);

  function changeOrigin(field, value) {
    copyAttempt.current = null;
    setCopyConflicts([]); setCopyMessage('');
    if (field === 'latitude') setLatitude(value); else setLongitude(value);
  }

  async function copyRoute() {
    if (copyBusy || snapshot.state !== 'ready') return;
    const lat = Number(latitude.replace(',', '.'));
    const lon = Number(longitude.replace(',', '.'));
    if (!latitude.trim() || !longitude.trim() || !Number.isFinite(lat) || Math.abs(lat) > 90 || !Number.isFinite(lon) || Math.abs(lon) > 180) {
      setCopyMessage('Укажите широту и долготу точки старта.'); return;
    }
    const initData = window.WebApp?.initData;
    if (typeof initData !== 'string' || !initData) {
      setCopyMessage('Чтобы создать свой план, откройте эту ссылку в MAX.'); return;
    }
    const controller = new AbortController(); copyRequest.current = controller;
    setCopyBusy(true); setCopyMessage(''); setCopyConflicts([]);
    try {
      if (!copyAttempt.current) copyAttempt.current = createCopyAttempt(snapshot.value, token,
        { latitude: lat, longitude: lon }, selectedUnknowns);
      const session = await exchangeMaxInitData(apiBaseUrl, initData, fetch, controller.signal);
      const result = await sendCopyCommand(apiBaseUrl, session.accessToken, copyAttempt.current, fetch, controller.signal);
      if (result.conflicts) {
        copyAttempt.current = null;
        setCopyConflicts(result.conflicts);
        setCopyMessage('Сейчас этот маршрут скопировать нельзя. Выберите другой старт или попросите автора обновить маршрут.');
        return;
      }
      const route = await loadOwnerRoute(apiBaseUrl, session.accessToken, result.route.route_id, fetch, controller.signal);
      if (!controller.signal.aborted) { copyAttempt.current = null; setCopied({ route, accessToken: session.accessToken }); }
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof RouteRequestError && error.status === 409) {
        copyAttempt.current = null;
        setCopyMessage('Маршрут автора изменился. Обновили просмотр — проверьте его и повторите копирование.');
        setAttempt((value) => value + 1);
      } else if (error instanceof RouteRequestError && error.status === 404) {
        copyAttempt.current = null;
        setSnapshot({ state: 'unavailable' });
      } else setCopyMessage('Не удалось создать план. Повторите запрос с теми же условиями.');
    } finally {
      if (!controller.signal.aborted) { setCopyBusy(false); copyRequest.current = null; }
    }
  }

  if (copied) return <OwnerRouteScreen route={copied.route} apiBaseUrl={apiBaseUrl} accessToken={copied.accessToken}
    mapApiKey={mapApiKey} backLabel="К общему маршруту" onBack={() => setCopied(null)} />;

  if (display) return <div className="owner-page shared-route-page">
    <RouteScreen route={display} mapApiKey={mapApiKey} shared />
    <section className="owner-route-actions" aria-label="Обновление общего маршрута">
      <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Обновить маршрут</button>
      <button className="scenario-option scenario-primary" onClick={() => setCopyOpen((value) => !value)}>Создать свой план</button>
    </section>
    {copyOpen && <section className="scenario-card scenario-form shared-copy-form" aria-label="Создание своего плана">
      <h2>Ваш старт</h2>
      <p>Маршрут будет пересчитан для вас пешком, без бюджета и личных настроек автора. Подтверждения участия и билеты автора не переходят в новый план.</p>
      <div className="scenario-endpoints">
        <label>Широта<input inputMode="decimal" value={latitude} onChange={(event) => changeOrigin('latitude', event.target.value)} placeholder="58.0100" /></label>
        <label>Долгота<input inputMode="decimal" value={longitude} onChange={(event) => changeOrigin('longitude', event.target.value)} placeholder="56.2500" /></label>
      </div>
      {mapApiKey && <button type="button" className="scenario-option" onClick={() => setPickOnMap((value) => !value)}>Выбрать старт на карте</button>}
      {pickOnMap && <TwoGisRouteMap apiKey={mapApiKey} city={snapshot.value.city} stops={[]} pickMode="start"
        onCancelPick={() => setPickOnMap(false)} onPick={([lat, lon]) => { changeOrigin('latitude', String(lat)); setLongitude(String(lon)); setPickOnMap(false); }} />}
      {unknowns.length > 0 && <fieldset><legend>Неизвестные условия</legend>
        {unknowns.map((item) => <label key={item.code}><input type="checkbox" checked={selectedUnknowns.includes(item.code)}
          onChange={() => { copyAttempt.current = null; setSelectedUnknowns((old) => old.includes(item.code) ? old.filter((code) => code !== item.code) : [...old, item.code]); }} />
          {item.message || item.code}</label>)}
      </fieldset>}
      <button type="button" className="scenario-option scenario-primary" disabled={copyBusy} onClick={copyRoute}>{copyBusy ? 'Создаём план…' : 'Скопировать маршрут'}</button>
      {copyMessage && <p role="status">{copyMessage}</p>}
      {copyConflicts.map((conflict, index) => <p key={`${conflict.code}-${index}`}>{userMessage(conflict)}</p>)}
    </section>}
  </div>;

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
