import React, { useEffect, useEffectEvent, useRef, useState } from 'react';
import { coordinate } from './routeProjection.js';
import { externalLegLink, openExternalNavigation } from './externalNavigation.js';

const radii = [300, 500, 800, 1000];

function validResults(body, radius) {
  return body?.radius_meters === radius && typeof body.request_id === 'string'
    && Number.isFinite(Date.parse(body.searched_at)) && Array.isArray(body.candidates) && body.candidates.length <= 20
    && body.candidates.every((item) => item?.provider === '2gis' && typeof item.external_id === 'string'
      && item.external_id.length > 0 && typeof item.title === 'string' && item.title.length > 0
      && coordinate(item.position) && Number.isInteger(item.distance_meters) && item.distance_meters >= 0
      && item.distance_meters <= radius && Number.isFinite(Date.parse(item.observed_at))
      && item.price_status === 'unknown' && item.hours_status === 'unknown' && item.availability_status === 'unknown'
      && (item.address === undefined || typeof item.address === 'string'))
    && new Set(body.candidates.map((item) => item.external_id)).size === body.candidates.length;
}

function searchMessage(status, code) {
  if (status === 401) return 'Сессия закончилась. Откройте Mini App заново в MAX.';
  if (status === 404) return 'Маршрут больше недоступен.';
  if (status === 429 || code === 'LUNCH_SEARCH_RATE_LIMITED' || code === 'LUNCH_SEARCH_BUSY') return 'Слишком много запросов. Попробуйте немного позже.';
  if (code === 'LUNCH_SEARCH_NOT_CONFIGURED' || code === 'LUNCH_SEARCH_ACCESS_DENIED') return 'Поиск кафе пока недоступен: источник не подключён или отклонил доступ.';
  return 'Не удалось получить кафе рядом. Попробуйте снова.';
}

export default function LunchSearch({ routeID, apiBaseUrl, accessToken, disabled, openRequest = 0 }) {
  const [open, setOpen] = useState(false);
  const [radius, setRadius] = useState(500);
  const [phase, setPhase] = useState('idle');
  const [message, setMessage] = useState('');
  const [results, setResults] = useState(null);
  const [detail, setDetail] = useState(null);
  const detailRequest = useRef(null);
  const current = useRef(null);
  const heading = useRef(null);
  const opener = useRef(null);
  const openFromTimeline = useEffectEvent(() => {
    search(radius);
    heading.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    heading.current?.focus({ preventScroll: true });
  });
  useEffect(() => () => { current.current?.abort(); detailRequest.current?.abort(); }, []);
  useEffect(() => { if (openRequest > 0) openFromTimeline(); }, [openRequest]);
  useEffect(() => {
    if (open) {
      heading.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      heading.current?.focus({ preventScroll: true });
    }
  }, [open]);

  function close() {
    detailRequest.current?.abort(); detailRequest.current = null; setDetail(null);
    current.current?.abort(); current.current = null;
    setOpen(false); setResults(null); setMessage(''); setPhase('idle');
    if (opener.current?.isConnected) opener.current.focus({ preventScroll: true });
    opener.current = null;
  }

  async function search(nextRadius) {
    if (disabled) return;
    detailRequest.current?.abort(); detailRequest.current = null; setDetail(null);
    if (!open) opener.current = document.activeElement;
    current.current?.abort();
    const controller = new AbortController();
    current.current = controller;
    setOpen(true); setRadius(nextRadius); setResults(null); setMessage(''); setPhase('locating');
    let timer;
    let timedOut = false;
    try {
      if (!navigator.geolocation) throw { geo: true };
      const position = await new Promise((resolve, reject) => navigator.geolocation.getCurrentPosition(
        ({ coords }) => resolve({ latitude: coords.latitude, longitude: coords.longitude }),
        () => reject({ geo: true }), { enableHighAccuracy: true, maximumAge: 0, timeout: 12000 },
      ));
      if (controller.signal.aborted) return;
      if (!coordinate(position)) throw { geo: true };
      setPhase('searching');
      timer = setTimeout(() => { timedOut = true; controller.abort(); }, 10000);
      const response = await fetch(`${apiBaseUrl}/api/v1/routes/${encodeURIComponent(routeID)}/lunch/search`, {
        method: 'POST', headers: { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' },
        credentials: 'omit', cache: 'no-store', signal: controller.signal,
        body: JSON.stringify({ position, radius_meters: nextRadius }),
      });
      const body = await response.json();
      if (controller.signal.aborted) throw new Error('Search cancelled');
      if (!response.ok) throw { status: response.status, code: body?.code };
      if (!validResults(body, nextRadius)) throw new Error('Invalid search response');
      setResults({ candidates: body.candidates, position }); setPhase('ready');
    } catch (error) {
      if (current.current !== controller || controller.signal.aborted && !timedOut) return;
      setMessage(error.geo ? 'Разрешите геолокацию, чтобы найти кафе рядом с вами.' : searchMessage(error.status, error.code));
      setPhase('error');
    } finally {
      clearTimeout(timer);
      if (current.current === controller) current.current = null;
    }
  }

  async function loadDetails(item) {
    if (disabled) return;
    detailRequest.current?.abort();
    const controller = new AbortController(); detailRequest.current = controller;
    setDetail({ id: item.external_id, phase: 'loading' });
    let timedOut = false;
    const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 10000);
    try {
      const response = await fetch(`${apiBaseUrl}/api/v1/routes/${encodeURIComponent(routeID)}/lunch/organizations/${encodeURIComponent(item.external_id)}`, {
        headers: { Authorization: `Bearer ${accessToken}` }, credentials: 'omit', cache: 'no-store', signal: controller.signal,
      });
      const body = await response.json();
      if (controller.signal.aborted) throw new Error('Details cancelled');
      if (!response.ok) throw { status: response.status, code: body?.code };
      const value = body?.organization;
      if (typeof body?.request_id !== 'string' || value?.provider !== '2gis' || value.external_id !== item.external_id
        || typeof value.title !== 'string' || !value.title.trim() || [...value.title].length > 500 || !coordinate(value.position)
        || !Number.isFinite(Date.parse(value.observed_at)) || value.price_status !== 'unknown'
        || value.hours_status !== 'unknown' || value.availability_status !== 'unknown'
        || value.address !== undefined && (typeof value.address !== 'string' || [...value.address].length > 1000)) throw new Error('Invalid organization response');
      if (detailRequest.current !== controller) return;
      setDetail({ id: item.external_id, phase: 'ready', value });
    } catch (error) {
      if (detailRequest.current !== controller || controller.signal.aborted && !timedOut) return;
      const message = error.status === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.'
        : error.code === 'LUNCH_ORGANIZATION_NOT_FOUND' ? 'Организация больше не найдена в 2ГИС. Обновите поиск.'
        : error.status === 429 || ['LUNCH_DETAILS_BUSY', 'LUNCH_DETAILS_RATE_LIMITED'].includes(error.code) ? 'Источник занят. Попробуйте немного позже.'
        : 'Не удалось обновить сведения. Ниже остаются данные поиска.';
      setDetail({ id: item.external_id, phase: 'error', message });
    } finally { clearTimeout(timer); }
  }

  const busy = phase === 'locating' || phase === 'searching';
  return <section className="owner-route-actions lunch-search" aria-label="Кафе рядом">
    {!open ? <button className="scenario-option lunch-search-open" disabled={disabled} onClick={() => search(radius)}>Обед · кафе рядом</button> : <div className="lunch-search-panel">
      <div className="lunch-search-heading"><h2 ref={heading} tabIndex={-1}>Где пообедать?</h2><button type="button" className="scenario-option" onClick={close}>Закрыть</button></div>
      <div className="lunch-search-radii" aria-label="Радиус поиска">{radii.map((value) => <button type="button" className="scenario-option" key={value} aria-pressed={radius === value} disabled={disabled} onClick={() => search(value)}>{value === 1000 ? '1 км' : `${value} м`}</button>)}</div>
      {busy && <p role="status">{phase === 'locating' ? 'Определяем вашу позицию…' : 'Ищем кафе рядом…'}</p>}
      {message && <p className="scenario-error" role="alert">{message}</p>}
      {phase === 'ready' && results.candidates.length === 0 && <p role="status">В этом радиусе ничего не найдено. Попробуйте увеличить радиус.</p>}
      {phase === 'ready' && results.candidates.length > 0 && <>
        <p className="lunch-search-note">Расстояние по прямой. Часы работы и цены уточните в кафе. Переход в 2ГИС не меняет расписание.</p>
        <ul className="lunch-search-list">{results.candidates.map((item) => {
          const selected = detail?.id === item.external_id ? detail : null;
          const value = selected?.phase === 'ready' ? selected.value : item;
          const link = externalLegLink({ mode: 'walk', verification: 'unknown' }, [[results.position.latitude, results.position.longitude], [value.position.latitude, value.position.longitude]]);
          return <li key={item.external_id}>
            <div className="lunch-search-place"><h3>{value.title}</h3>{!selected?.value && <span>{item.distance_meters} м</span>}</div>
            {value.address && <div className="lunch-search-address">{value.address}</div>}
            <button type="button" className="scenario-option" disabled={disabled || selected?.phase === 'loading'} onClick={() => loadDetails(item)}>{selected?.phase === 'loading' ? 'Получаем сведения…' : selected?.phase === 'error' ? 'Повторить сведения' : 'Сведения о заведении'}</button>
            {selected?.phase === 'ready' && <p className="lunch-search-note" role="status">Сведения обновлены из 2ГИС. Часы работы и цены не подтверждены.</p>}
            {selected?.phase === 'error' && <p className="scenario-error" role="alert">{selected.message}</p>}
            <a className="scenario-option" href={link} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" onClick={openExternalNavigation}>Как дойти · 2ГИС</a>
          </li>;
        })}</ul>
      </>}
      <button type="button" className="scenario-option" disabled={disabled || busy} onClick={() => search(radius)}>Обновить рядом со мной</button>
    </div>}
  </section>;
}
