import React, { useEffect, useRef, useState } from 'react';
import { fitPoints, loadMapGL } from './TwoGisRouteMap.jsx';
import { externalLegLink, openExternalNavigation } from './externalNavigation.js';
import { coordinate } from './routeProjection.js';
import RouteNavigation from './RouteNavigation.jsx';

function validSegments(segments) {
  return Array.isArray(segments) && segments.length > 0 && segments.every((segment) => Array.isArray(segment) && segment.length >= 2
    && segment.every((point) => Array.isArray(point) && point.length === 2 && point.every(Number.isFinite) && Math.abs(point[0]) <= 90 && Math.abs(point[1]) <= 180));
}

const maxRememberedPaths = 200;
const rememberedPaths = new Map();
let directionsLimitedUntil = 0;

function samePoint(left, right) {
  return left[0] === right[0] && left[1] === right[1];
}

// A route is reloaded after every command; asking the provider again for legs that did not move would
// spend its request limit on answers we already hold. Only successful answers are kept.
async function streetPath(apiBaseUrl, points, mode, signal) {
  const key = `${apiBaseUrl}|${mode}|${points.map((point) => point.join(',')).join('|')}`;
  const known = rememberedPaths.get(key);
  if (known) return known;
  if (Date.now() < directionsLimitedUntil) throw new Error('DIRECTIONS_RATE_LIMITED');
  const response = await fetch(`${apiBaseUrl}/api/v1/prototype/directions`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, signal, body: JSON.stringify({ points, mode }),
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    if (body?.code === 'DIRECTIONS_RATE_LIMITED' || response.status === 429) {
      directionsLimitedUntil = Date.now() + 60_000;
      throw new Error('DIRECTIONS_RATE_LIMITED');
    }
    throw new Error('Directions unavailable');
  }
  const { segments } = await response.json();
  if (!validSegments(segments)) throw new Error('Invalid directions');
  if (rememberedPaths.size >= maxRememberedPaths) rememberedPaths.delete(rememberedPaths.keys().next().value);
  rememberedPaths.set(key, segments);
  return segments;
}

export default function ServerRouteMap({ apiKey, apiBaseUrl = '', projection, lunchPreview, onClearLunch, onSelect }) {
  const container = useRef(null);
  const mapRef = useRef(null);
  const mapglRef = useRef(null);
  const select = useRef(onSelect);
  const [state, setState] = useState('loading');
  const [attempt, setAttempt] = useState(0);
  const [pathState, setPathState] = useState('loading');
  const [lunchPathState, setLunchPathState] = useState('idle');
  select.current = onSelect;
  useEffect(() => {
    if (!apiKey) { setState('no-key'); return; }
    let active = true;
    let map;
    const controller = new AbortController();
    const objects = [];
    setState('loading');
    setPathState('loading');
    const points = [...projection.segments.flatMap((segment) => segment.points), ...projection.visits.flatMap((visit) => visit.point ? [visit.point] : []), ...[projection.origin, projection.destination].filter(Boolean)];
    if (!points.length) { setState('empty'); return; }
    loadMapGL().then((mapgl) => {
      if (!active) return;
      map = new mapgl.Map(container.current, { center: [points[0][1], points[0][0]], zoom: 13, key: apiKey, enableTrackResize: true, zoomControl: 'centerLeft' });
      map.on('error', () => { if (active) setState('error'); });
      async function drawPaths() {
        let failed = 0;
        let built = 0;
        let rateLimited = false;
        const jobs = [];
        for (const leg of projection.legs) {
          if (!active) return;
          const saved = projection.segments.find((segment) => segment.leg === leg)?.points;
          if (saved?.length > 2) {
            jobs.push({ saved });
            continue;
          }
          const from = saved?.[0] || (leg.from_kind === 'origin' ? projection.origin : projection.visits.find((visit) => visit.id === leg.from_visit_id)?.point);
          const to = saved?.at(-1) || (leg.to_kind === 'destination' ? projection.destination : projection.visits.find((visit) => visit.id === leg.to_visit_id)?.point);
          if (!from || !to || leg.verification === 'unavailable') { failed++; continue; }
          if (samePoint(from, to)) continue;
          const mode = leg.mode === 'car' ? 'driving' : leg.mode === 'transit' ? 'transit' : 'walking';
          const previous = jobs.at(-1);
          if (mode !== 'transit' && previous?.mode === mode && previous.points.length < (mode === 'walking' ? 5 : 10) && samePoint(previous.points.at(-1), from)) {
            previous.points.push(to);
          } else {
            jobs.push({ mode, points: [from, to] });
          }
        }
        for (const job of jobs) {
          if (!active) return;
          if (job.saved) {
            objects.push(new mapgl.Polyline(map, { coordinates: job.saved.map(([lat, lon]) => [lon, lat]), width: 5, color: '#087af5' }));
            built++;
            continue;
          }
          try {
            const segments = await streetPath(apiBaseUrl, job.points, job.mode, controller.signal);
            if (!active) return;
            segments.forEach((segment) => objects.push(new mapgl.Polyline(map, { coordinates: segment.map(([lat, lon]) => [lon, lat]), width: 6, color: '#087af5' })));
            built++;
          } catch (error) {
            if (!active) return;
            failed++;
            if (error.message === 'DIRECTIONS_RATE_LIMITED') { rateLimited = true; break; }
          }
        }
        if (active) setPathState(rateLimited ? 'rate-limited' : failed ? built ? 'partial' : 'error' : 'ready');
      }
      drawPaths();
      projection.visits.filter((visit) => visit.point).forEach((visit) => {
        const button = document.createElement('button');
        button.type = 'button'; button.className = `workspace-map-pin${visit.completed ? ' is-completed' : ''}`;
        button.textContent = String(visit.number); button.title = visit.title;
        button.setAttribute('aria-label', `${visit.number}. ${visit.title}`);
        button.addEventListener('click', () => select.current(visit.id));
        objects.push(new mapgl.HtmlMarker(map, { coordinates: [visit.point[1], visit.point[0]], html: button, anchor: [19, 19], interactive: true }));
      });
      [[projection.origin, 'Старт'], [projection.destination, 'Финиш']].forEach(([point, title]) => {
        if (!point) return;
        const pin = document.createElement('span'); pin.className = 'server-map-endpoint'; pin.textContent = title;
        objects.push(new mapgl.HtmlMarker(map, { coordinates: [point[1], point[0]], html: pin, interactive: false }));
      });
      fitPoints(map, points);
      mapRef.current = map;
      mapglRef.current = mapgl;
      setState('ready');
    }).catch(() => { if (active) setState('error'); });
    return () => { active = false; controller.abort(); objects.forEach((object) => object.destroy()); if (mapRef.current === map) { mapRef.current = null; mapglRef.current = null; } map?.destroy(); };
  }, [apiKey, apiBaseUrl, projection, attempt]);

  useEffect(() => {
    if (!lunchPreview || state !== 'ready' || !mapRef.current || !mapglRef.current) {
      setLunchPathState('idle');
      return;
    }
    const origin = coordinate(lunchPreview.origin);
    const cafe = coordinate(lunchPreview.cafe?.position);
    if (!origin || !cafe) { setLunchPathState('error'); return; }
    const map = mapRef.current;
    const mapgl = mapglRef.current;
    const controller = new AbortController();
    const objects = [];
    let active = true;
    const cafePin = document.createElement('span');
    cafePin.className = 'server-map-lunch-pin';
    cafePin.textContent = 'Обед';
    cafePin.title = lunchPreview.cafe.title;
    objects.push(new mapgl.HtmlMarker(map, { coordinates: [cafe[1], cafe[0]], html: cafePin, interactive: false }));
    const originPin = document.createElement('span');
    originPin.className = 'server-map-lunch-origin';
    originPin.textContent = 'Вы здесь';
    objects.push(new mapgl.HtmlMarker(map, { coordinates: [origin[1], origin[0]], html: originPin, interactive: false }));
    fitPoints(map, [origin, cafe]);
    setLunchPathState('loading');
    async function drawWalk() {
      try {
        const segments = await streetPath(apiBaseUrl, [origin, cafe], 'walking', controller.signal);
        if (!active) return;
        segments.forEach((segment) => objects.push(new mapgl.Polyline(map, { coordinates: segment.map(([lat, lon]) => [lon, lat]), width: 7, color: '#dc8b19' })));
        setLunchPathState('ready');
      } catch { if (active) setLunchPathState('error'); }
    }
    drawWalk();
    return () => { active = false; controller.abort(); objects.forEach((object) => object.destroy()); };
  }, [apiBaseUrl, lunchPreview, state, attempt]);

  const missing = projection.visits.filter((visit) => !visit.point).length;
  const lunchOrigin = coordinate(lunchPreview?.origin);
  const lunchCafe = coordinate(lunchPreview?.cafe?.position);
  const lunchLink = lunchOrigin && lunchCafe ? externalLegLink({ mode: 'walk', verification: 'unknown' }, [lunchOrigin, lunchCafe]) : null;
  return <section className="workspace-map-column workspace-map" aria-label="Карта маршрута">
    <div className="workspace-map-top"><span>Карта маршрута</span><span>2ГИС</span></div>
    <div className="workspace-map-canvas"><div ref={container} className="workspace-2gis-map" aria-hidden={state !== 'ready'} />
      {state !== 'ready' && <div className="workspace-map-state server-map-state" role="status"><p>{state === 'loading' ? 'Загружаем карту…' : state === 'no-key' ? 'Карта не настроена. Расписание доступно ниже.' : state === 'empty' ? 'В маршруте нет координат для карты.' : 'Не удалось загрузить карту. Расписание доступно ниже.'}</p>{state === 'error' && <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button>}</div>}
    </div>
    <div className="workspace-map-caption"><span>{pathState === 'loading' ? 'Строим путь по улицам…' : pathState === 'ready' ? 'Путь по улицам' : pathState === 'rate-limited' ? 'Лимит запросов 2ГИС. Откройте переход в 2ГИС и повторите позже.' : pathState === 'partial' ? 'Часть пути недоступна. Можно открыть переход в 2ГИС.' : 'Путь не загрузился. Попробуйте снова или откройте 2ГИС.'}{missing ? ` Без координат: ${missing} точек.` : ''}</span>{['error', 'partial'].includes(pathState) && <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button>}</div>
    {lunchPreview && <div className="server-map-lunch-preview" role="status"><div><span>Выбрано кафе</span><strong>{lunchPreview.cafe.title}</strong><small>{state !== 'ready' ? 'Карта пока недоступна. Кафе можно открыть в 2ГИС.' : lunchPathState === 'loading' ? 'Строим пеший путь…' : lunchPathState === 'ready' ? 'Пеший путь от вашей позиции показан на карте' : lunchPathState === 'error' ? 'Путь по улицам пока недоступен. Кафе показано на карте.' : 'Кафе показано на карте.'}</small><small>Это просмотр: расписание маршрута пока не изменилось.</small></div><div className="server-map-lunch-actions">{lunchLink && <a href={lunchLink} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" onClick={openExternalNavigation}>Открыть в 2ГИС</a>}<button type="button" onClick={onClearLunch}>Убрать с карты</button></div></div>}
    <RouteNavigation projection={projection} />
  </section>;
}
