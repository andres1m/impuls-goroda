import React, { useEffect, useEffectEvent, useRef, useState } from 'react';
import { coordinate } from './routeProjection.js';
import { externalLegLink, openExternalNavigation } from './externalNavigation.js';
import { localDateTime } from './scenarioForm.js';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';

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

export default function LunchSearch({ routeID, city, plan, apiBaseUrl, accessToken, mapApiKey, disabled, onChoose, onSchedule, openRequest = 0, hideLauncher = false }) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState('nearby');
  const [radius, setRadius] = useState(500);
  const [phase, setPhase] = useState('idle');
  const [message, setMessage] = useState('');
  const [results, setResults] = useState(null);
  const [searchSource, setSearchSource] = useState('device');
  const [selectedPosition, setSelectedPosition] = useState(null);
  const [picking, setPicking] = useState(false);
  const [manualLatitude, setManualLatitude] = useState('');
  const [manualLongitude, setManualLongitude] = useState('');
  const [lunchStart, setLunchStart] = useState(() => localDateTime(plan?.constraints?.lunch_window?.start_at, plan?.timezone).slice(11) || '13:00');
  const [lunchEnd, setLunchEnd] = useState(() => localDateTime(plan?.constraints?.lunch_window?.end_at, plan?.timezone).slice(11) || '14:30');
  const [lunchDuration, setLunchDuration] = useState(() => String(plan?.constraints?.lunch_window?.min_duration_seconds || 2700));
  const [scheduleError, setScheduleError] = useState('');
  const current = useRef(null);
  const heading = useRef(null);
  const opener = useRef(null);
  const openFromTimeline = useEffectEvent(() => {
    search(radius);
    heading.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    heading.current?.focus({ preventScroll: true });
  });
  useEffect(() => () => current.current?.abort(), []);
  useEffect(() => { if (openRequest > 0) openFromTimeline(); }, [openRequest]);
  useEffect(() => {
    if (open) {
      heading.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      heading.current?.focus({ preventScroll: true });
    }
  }, [open]);

  function close() {
    current.current?.abort(); current.current = null;
    setOpen(false); setMode('nearby'); setResults(null); setMessage(''); setPhase('idle'); setScheduleError(''); setPicking(false);
    setSearchSource('device'); setSelectedPosition(null); setManualLatitude(''); setManualLongitude('');
    if (opener.current?.isConnected) opener.current.focus({ preventScroll: true });
    opener.current = null;
  }

  function choose(item) {
    if (disabled || !results?.position || !onChoose) return;
    onChoose({ cafe: item, origin: results.position });
    close();
  }

  function showSchedule() {
    current.current?.abort(); current.current = null;
    setMode('time'); setScheduleError(''); setPicking(false);
  }

  async function schedule(event) {
    event.preventDefault();
    if (disabled || !onSchedule) return;
    setScheduleError('');
    try {
      const outcome = await onSchedule({ lunchStart, lunchEnd, lunchDuration });
      if (outcome?.ok) { close(); requestAnimationFrame(() => document.querySelector('.owner-variants')?.scrollIntoView({ behavior: 'smooth', block: 'center' })); }
      else setScheduleError(outcome?.message || 'Не удалось рассчитать новый вариант. Попробуйте снова.');
    } catch { setScheduleError('Не удалось рассчитать новый вариант. Попробуйте снова.'); }
  }

  function choosePosition(position) {
    if (!coordinate(position)) return;
    setSelectedPosition(position); setSearchSource('selected'); setPicking(false);
    setManualLatitude(String(position.latitude)); setManualLongitude(String(position.longitude));
    search(radius, position);
  }

  async function search(nextRadius, overridePosition, forceDevice = false) {
    if (disabled) return;
    if (!open) opener.current = document.activeElement;
    current.current?.abort();
    const controller = new AbortController();
    current.current = controller;
    const chosen = overridePosition || (!forceDevice && searchSource === 'selected' ? selectedPosition : null);
    setOpen(true); setMode('nearby'); setRadius(nextRadius); setResults(null); setMessage(''); setPhase(chosen ? 'searching' : 'locating');
    let timer;
    let timedOut = false;
    try {
      const position = chosen || await new Promise((resolve, reject) => {
        if (!navigator.geolocation) { reject({ geo: true }); return; }
        navigator.geolocation.getCurrentPosition(
          ({ coords }) => resolve({ latitude: coords.latitude, longitude: coords.longitude }),
          () => reject({ geo: true }), { enableHighAccuracy: true, maximumAge: 0, timeout: 12000 },
        );
      });
      if (controller.signal.aborted) return;
      if (!coordinate(position)) throw { geo: true };
      setPhase('searching');
      timer = setTimeout(() => { timedOut = true; controller.abort(); }, 15000);
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
      setMessage(error.geo ? 'Геопозиция недоступна. Выберите точку на карте или укажите координаты.' : searchMessage(error.status, error.code));
      setPhase('error');
    } finally {
      clearTimeout(timer);
      if (current.current === controller) current.current = null;
    }
  }

  const busy = phase === 'locating' || phase === 'searching';
  return <section className="owner-route-actions lunch-search" aria-label="Кафе рядом">
    {!open ? !hideLauncher && <button className="scenario-option lunch-search-open" disabled={disabled} onClick={() => search(radius)}>Обед · кафе рядом</button> : <div className="lunch-search-panel">
      <div className="lunch-search-heading"><h2 ref={heading} tabIndex={-1}>Где пообедать?</h2><button type="button" className="scenario-option" onClick={close}>Закрыть</button></div>
      <div className="lunch-search-modes" aria-label="Способ выбора обеда"><button type="button" className="scenario-option" aria-pressed={mode === 'nearby'} disabled={disabled} onClick={() => search(radius)}>Кафе рядом сейчас</button><button type="button" className="scenario-option" aria-pressed={mode === 'time'} disabled={disabled} onClick={showSchedule}>Запланировать время</button></div>
      {mode === 'time' ? <form className="lunch-time-form" onSubmit={schedule}>
        <p>Выберите время обеда в день маршрута. Будет рассчитан новый вариант; текущий маршрут сохранится, точки могут измениться.</p>
        <div className="lunch-time-fields"><label>Начать не раньше<input type="time" value={lunchStart} required disabled={disabled} onChange={(event) => setLunchStart(event.target.value)} /></label><label>Закончить не позже<input type="time" value={lunchEnd} required disabled={disabled} onChange={(event) => setLunchEnd(event.target.value)} /></label></div>
        <label>Длительность<select value={lunchDuration} disabled={disabled} onChange={(event) => setLunchDuration(event.target.value)}><option value="2700">45 минут</option><option value="3600">60 минут</option></select></label>
        {scheduleError && <p className="scenario-error" role="alert">{scheduleError}</p>}
        <button type="submit" className="scenario-option scenario-primary" disabled={disabled}>Рассчитать новый вариант</button>
      </form> : <>
      <div className="lunch-search-origin" aria-label="Центр поиска">
        <button type="button" className="scenario-option" aria-pressed={searchSource === 'device'} disabled={disabled} onClick={() => { setSearchSource('device'); setPicking(false); search(radius, null, true); }}>Моя геопозиция</button>
        {mapApiKey && <button type="button" className="scenario-option" aria-pressed={searchSource === 'selected'} disabled={disabled} onClick={() => { current.current?.abort(); setResults(null); setPhase('idle'); setPicking(true); }}>Указать на карте</button>}
        {coordinate(plan?.origin) && <button type="button" className="scenario-option" disabled={disabled} onClick={() => choosePosition(plan.origin)}>У старта маршрута</button>}
      </div>
      {picking && <TwoGisRouteMap apiKey={mapApiKey} city={city} stops={[]} startPoint={coordinate(plan?.origin)} pickMode="search" onCancelPick={() => setPicking(false)} onPick={([latitude, longitude]) => choosePosition({ latitude, longitude })} />}
      <div className="lunch-search-manual"><label>Широта<input inputMode="decimal" value={manualLatitude} onChange={(event) => setManualLatitude(event.target.value)} placeholder="58.0100" /></label><label>Долгота<input inputMode="decimal" value={manualLongitude} onChange={(event) => setManualLongitude(event.target.value)} placeholder="56.2500" /></label><button type="button" className="scenario-option" disabled={disabled || !coordinate({ latitude: Number(manualLatitude), longitude: Number(manualLongitude) }) || !manualLatitude.trim() || !manualLongitude.trim()} onClick={() => choosePosition({ latitude: Number(manualLatitude), longitude: Number(manualLongitude) })}>Искать от точки</button></div>
      <div className="lunch-search-radii" aria-label="Радиус поиска">{radii.map((value) => <button type="button" className="scenario-option" key={value} aria-pressed={radius === value} disabled={disabled} onClick={() => search(value)}>{value === 1000 ? '1 км' : `${value} м`}</button>)}</div>
      {busy && <p role="status">{phase === 'locating' ? 'Определяем вашу позицию…' : 'Ищем кафе рядом…'}</p>}
      {message && <p className="scenario-error" role="alert">{message}</p>}
      {phase === 'ready' && results.candidates.length === 0 && <p role="status">В этом радиусе ничего не найдено. Попробуйте увеличить радиус.</p>}
      {phase === 'ready' && results.candidates.length > 0 && <>
        <p className="lunch-search-note">Кафе можно посмотреть на карте. В расписание оно пока не добавляется; цены и часы работы уточните в заведении.</p>
        <ul className="lunch-search-list">{results.candidates.map((item) => {
          const link = externalLegLink({ mode: 'walk', verification: 'unknown' }, [[results.position.latitude, results.position.longitude], [item.position.latitude, item.position.longitude]]);
          return <li key={item.external_id}>
            <button type="button" className="lunch-search-place" disabled={disabled} onClick={() => choose(item)}><span className="lunch-search-place-main"><strong>{item.title}</strong><small>{item.address || 'Адрес не указан'} · {item.distance_meters} м по прямой</small></span><span className="lunch-search-place-action">Показать на карте</span></button>
            <a className="lunch-search-directions" href={link} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" onClick={openExternalNavigation}>Как дойти в 2ГИС</a>
          </li>;
        })}</ul>
      </>}
      <button type="button" className="scenario-option" disabled={disabled || busy} onClick={() => search(radius)}>{searchSource === 'selected' ? 'Обновить от выбранной точки' : 'Обновить рядом со мной'}</button>
      </>}
    </div>}
  </section>;
}
