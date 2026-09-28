import React, { useEffect, useRef, useState } from 'react';

let mapLoading;

function loadMapGL() {
  if (window.mapgl) return Promise.resolve(window.mapgl);
  if (mapLoading) return mapLoading;
  mapLoading = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = 'https://mapgl.2gis.com/api/js/v1';
    script.async = true;
    script.onload = () => window.mapgl ? resolve(window.mapgl) : reject(new Error('MapGL unavailable'));
    script.onerror = () => reject(new Error('MapGL unavailable'));
    document.head.append(script);
  }).catch((error) => { mapLoading = null; throw error; });
  return mapLoading;
}

async function catalogRequest(path, parameters, apiKey, signal) {
  const url = new URL(`https://catalog.api.2gis.com${path}`);
  Object.entries(parameters).forEach(([name, value]) => url.searchParams.set(name, String(value)));
  url.searchParams.set('key', apiKey);
  const response = await fetch(url, { signal });
  if (response.status === 401 || response.status === 403) throw new Error('CATALOG_ACCESS_DENIED');
  if (response.status === 429) throw new Error('CATALOG_RATE_LIMITED');
  if (!response.ok) throw new Error('CATALOG_UNAVAILABLE');
  const data = await response.json();
  if (data.meta?.code === 401 || data.meta?.code === 403) throw new Error('CATALOG_ACCESS_DENIED');
  if (data.meta?.code === 429) throw new Error('CATALOG_RATE_LIMITED');
  if (data.meta?.code !== 200) throw new Error('CATALOG_UNAVAILABLE');
  return data.result?.items || [];
}

function validPoint(point) {
  return Array.isArray(point) && point.length === 2 && point.every(Number.isFinite);
}

function distanceMeters(first, second) {
  const radians = Math.PI / 180;
  const lat = (second[0] - first[0]) * radians;
  const lon = (second[1] - first[1]) * radians;
  const a = Math.sin(lat / 2) ** 2 + Math.cos(first[0] * radians) * Math.cos(second[0] * radians) * Math.sin(lon / 2) ** 2;
  return Math.round(12742000 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a)));
}

function fitPoints(map, points) {
  if (!points.length) return;
  const latitudes = points.map((point) => point[0]);
  const longitudes = points.map((point) => point[1]);
  if (points.length === 1) {
    map.setCenter([longitudes[0], latitudes[0]]);
    map.setZoom(14);
    return;
  }
  map.fitBounds({
    northEast: [Math.max(...longitudes), Math.max(...latitudes)],
    southWest: [Math.min(...longitudes), Math.min(...latitudes)],
  }, { padding: { top: 35, right: 35, bottom: 35, left: 35 } });
}

function twoGisRouteUrl(points, movement) {
  const type = movement === 'transit' ? 'bus' : movement === 'car' ? 'car' : 'pedestrian';
  const positions = points.map(([lat, lon]) => `${lon.toFixed(6)},${lat.toFixed(6)};`).join('|');
  return `https://2gis.ru/directions/tab/${type}/points/${positions}`;
}

export default function TwoGisRouteMap({ apiKey, city, stops, startPlace, finishPlace, startPoint, finishPoint, lunches = [], lunchNavigation, movement, pickMode, onPick, onCancelPick, selectedID, onSelect, radius, cafeCenter, onCafes }) {
  const container = useRef(null);
  const mapRef = useRef(null);
  const apiRef = useRef(null);
  const objects = useRef([]);
  const walkingObjects = useRef([]);
  const onSelectRef = useRef(onSelect);
  const onCafesRef = useRef(onCafes);
  const onPickRef = useRef(onPick);
  const [state, setState] = useState(apiKey ? 'loading' : 'no-key');
  const [routeState, setRouteState] = useState('idle');
  const [routeIssue, setRouteIssue] = useState('');
  const [routeLink, setRouteLink] = useState('');
  const [walkingState, setWalkingState] = useState('idle');
  const [ready, setReady] = useState(0);
  onSelectRef.current = onSelect;
  onCafesRef.current = onCafes;
  onPickRef.current = onPick;

  useEffect(() => {
    if (!apiKey) { setState('no-key'); return; }
    let active = true;
    setState('loading');
    loadMapGL().then((mapgl) => {
      if (!active) return;
      apiRef.current = mapgl;
      mapRef.current = new mapgl.Map(container.current, {
        center: city === 'perm' ? [56.25, 58.01] : [37.62, 55.75],
        zoom: 12,
        key: apiKey,
        enableTrackResize: true,
        zoomControl: 'centerLeft',
      });
      mapRef.current.on('error', () => { if (active) setState('error'); });
      setState('ready');
      setReady((value) => value + 1);
    }).catch(() => { if (active) setState('error'); });
    return () => {
      active = false;
      objects.current.forEach((item) => item.destroy());
      objects.current = [];
      walkingObjects.current.forEach((item) => item.destroy());
      walkingObjects.current = [];
      mapRef.current?.destroy();
      mapRef.current = null;
      apiRef.current = null;
    };
  }, [apiKey, city]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !pickMode) return;
    const controller = new AbortController();
    const handlePick = async (event) => {
      const [lon, lat] = event.lngLat || [];
      if (!Number.isFinite(lon) || !Number.isFinite(lat)) return;
      let address = '';
      try {
        const items = await catalogRequest('/3.0/items/geocode', { lon, lat, fields: 'items.address', locale: 'ru_RU' }, apiKey, controller.signal);
        address = items[0]?.full_name || items[0]?.address_name || items[0]?.name || '';
      } catch {
        if (controller.signal.aborted) return;
      }
      if (!controller.signal.aborted) onPickRef.current([lat, lon], address || `${lat.toFixed(6)}, ${lon.toFixed(6)}`);
    };
    map.on('click', handlePick);
    return () => { controller.abort(); map.off('click', handlePick); };
  }, [ready, pickMode, apiKey]);

  useEffect(() => {
    const map = mapRef.current;
    const mapgl = apiRef.current;
    if (!map || !mapgl) return;
    let active = true;
    const controller = new AbortController();
    objects.current.forEach((item) => item.destroy());
    objects.current = [];
    setRouteLink('');
    setRouteState('loading');
    setRouteIssue('');
    const shown = stops.filter((stop) => !stop.removed && !stop.pause);
    if (shown.length < 2) { setRouteState('missing-points'); return; }
    const cityName = city === 'perm' ? 'Пермь' : 'Москва';
    const references = [];
    if (startPoint || startPlace?.trim()) references.push({ point: startPoint || `${cityName}, ${startPlace}`, address: true });
    shown.forEach((stop, index) => {
      references.push({ point: stop.routePoint || `${cityName}, ${stop.title}`, stop });
      lunches.filter((item) => item.afterStopID === stop.id && validPoint(item.cafe?.coordinates)).forEach((item) => references.push({ point: item.cafe.coordinates }));
    });
    if (finishPoint || finishPlace?.trim()) references.push({ point: finishPoint || `${cityName}, ${finishPlace}`, address: true });

    function addStopMarkers(located) {
      located.forEach(({ stop, coordinates }) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'workspace-map-pin';
        button.setAttribute('aria-label', `${shown.indexOf(stop) + 1}. ${stop.title}`);
        button.title = stop.title;
        button.textContent = String(shown.indexOf(stop) + 1);
        button.addEventListener('click', () => onSelectRef.current(stop.id));
        const marker = new mapgl.HtmlMarker(map, {
          coordinates: [coordinates[1], coordinates[0]],
          html: button,
          anchor: [19, 19],
          interactive: true,
        });
        objects.current.push(marker);
      });
      lunches.filter((item) => validPoint(item.cafe?.coordinates)).forEach(({ cafe: lunchCafe }) => {
        const pin = document.createElement('div');
        pin.className = 'workspace-map-lunch-pin';
        pin.textContent = '☕';
        pin.setAttribute('aria-label', `Обед: ${lunchCafe.name}`);
        objects.current.push(new mapgl.HtmlMarker(map, {
          coordinates: [lunchCafe.coordinates[1], lunchCafe.coordinates[0]],
          html: pin, anchor: [20, 20], interactive: false,
        }));
      });
      if (!lunchNavigation) fitPoints(map, [...located.map((item) => item.coordinates), ...lunches.filter((item) => validPoint(item.cafe?.coordinates)).map((item) => item.cafe.coordinates)]);
    }

    async function loadRoute() {
      try {
        const coordinates = await Promise.all(references.map(async ({ point, address }) => {
          if (validPoint(point)) return point;
          const path = address ? '/3.0/items/geocode' : '/3.0/items';
          const items = await catalogRequest(path, { q: point, fields: 'items.point', locale: 'ru_RU' }, apiKey, controller.signal);
          const found = items[0]?.point;
          return Number.isFinite(found?.lat) && Number.isFinite(found?.lon) ? [found.lat, found.lon] : null;
        }));
        if (!active) return;
        if (coordinates.some((point) => !point)) {
          addStopMarkers(references.flatMap((reference, index) => reference.stop && coordinates[index] ? [{ stop: reference.stop, coordinates: coordinates[index] }] : []));
          setRouteIssue('Не удалось определить координаты всех остановок.');
          setRouteState('unavailable');
          return;
        }
        const response = await fetch('/api/v1/prototype/directions', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ points: coordinates, mode: movement === 'transit' ? 'transit' : movement === 'car' ? 'driving' : 'walking' }),
          signal: controller.signal,
        });
        if (!response.ok) {
          const error = await response.json().catch(() => null);
          throw new Error(error?.code || 'DIRECTIONS_UNAVAILABLE');
        }
        const result = await response.json();
        if (!active) return;
        if (!Array.isArray(result.segments) || !result.segments.length || result.segments.some((segment) => !Array.isArray(segment) || segment.length < 2)) throw new Error('DIRECTIONS_UNAVAILABLE');
        result.segments.forEach((segment) => {
          objects.current.push(new mapgl.Polyline(map, { coordinates: segment.map(([lat, lon]) => [lon, lat]), width: 6, color: '#1677df' }));
        });
        addStopMarkers(references.flatMap((reference, index) => reference.stop ? [{ stop: reference.stop, coordinates: coordinates[index] }] : []));
        if (!lunchNavigation) fitPoints(map, coordinates);
        setRouteLink(twoGisRouteUrl(coordinates, movement));
        setRouteState('ready');
      } catch (error) {
        if (!active) return;
        objects.current.forEach((item) => item.destroy());
        objects.current = [];
        addStopMarkers(shown.filter((stop) => validPoint(stop.routePoint)).map((stop) => ({ stop, coordinates: stop.routePoint })));
        setRouteIssue(error.message === 'DIRECTIONS_ACCESS_DENIED' ? '2ГИС отклонил ключ или доступ к маршрутам.' :
          error.message === 'DIRECTIONS_RATE_LIMITED' ? 'Лимит запросов к маршрутам 2ГИС исчерпан.' :
            error.message === 'DIRECTIONS_REJECTED_REQUEST' ? '2ГИС отклонил параметры маршрута.' :
              error.message === 'DIRECTIONS_NO_ROUTE' ? '2ГИС не нашёл путь через выбранные точки.' :
                'Маршрут сейчас недоступен.');
        setRouteState('request-error');
      }
    }
    loadRoute();
    return () => { active = false; controller.abort(); };
  }, [ready, city, stops.map((stop) => `${stop.id}:${stop.removed}:${stop.pause}`).join(','), startPlace, finishPlace, startPoint?.join(','), finishPoint?.join(','), JSON.stringify(lunches.map((item) => [item.id, item.afterStopID, item.cafe?.coordinates])), movement, apiKey]);

  useEffect(() => {
    walkingObjects.current.forEach((item) => item.destroy());
    walkingObjects.current = [];
    if (!lunchNavigation) { setWalkingState('idle'); return; }
    const map = mapRef.current;
    const mapgl = apiRef.current;
    if (!map || !mapgl) { setWalkingState('loading'); return; }
    const { origin, cafe } = lunchNavigation;
    if (!validPoint(origin) || !validPoint(cafe?.coordinates)) { setWalkingState('error'); return; }
    const controller = new AbortController();
    const pin = document.createElement('div');
    pin.className = 'workspace-map-user-pin';
    pin.textContent = 'Вы';
    pin.setAttribute('aria-label', 'Ваше положение при выборе обеда');
    walkingObjects.current.push(new mapgl.HtmlMarker(map, {
      coordinates: [origin[1], origin[0]], html: pin, anchor: [19, 19], interactive: false,
    }));
    fitPoints(map, [origin, cafe.coordinates]);
    setWalkingState('loading');
    async function loadWalkingRoute() {
      try {
        const response = await fetch('/api/v1/prototype/directions', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ points: [origin, cafe.coordinates], mode: 'walking' }),
          signal: controller.signal,
        });
        if (!response.ok) throw new Error('Walking route unavailable');
        const result = await response.json();
        if (controller.signal.aborted) return;
        if (!Array.isArray(result.segments) || !result.segments.length || result.segments.some((segment) => !Array.isArray(segment) || segment.length < 2 || segment.some((point) => !validPoint(point)))) throw new Error('Walking route unavailable');
        result.segments.forEach((segment) => walkingObjects.current.push(new mapgl.Polyline(map, {
          coordinates: segment.map(([lat, lon]) => [lon, lat]), width: 7, color: '#d78c16',
        })));
        setWalkingState('ready');
      } catch {
        if (!controller.signal.aborted) setWalkingState('error');
      }
    }
    loadWalkingRoute();
    return () => {
      controller.abort();
      walkingObjects.current.forEach((item) => item.destroy());
      walkingObjects.current = [];
    };
  }, [ready, lunchNavigation?.origin?.join(','), lunchNavigation?.cafe?.id]);

  useEffect(() => {
    if (!radius || !validPoint(cafeCenter) || !apiKey) return;
    const controller = new AbortController();
    const map = mapRef.current;
    if (map) {
      map.setCenter([cafeCenter[1], cafeCenter[0]]);
      map.setZoom(radius <= 500 ? 15 : 14);
    }
    const point = `${cafeCenter[1]},${cafeCenter[0]}`;
    const parameters = { q: 'кафе', type: 'branch', point, radius, location: point, sort: 'distance', page_size: 20, locale: 'ru_RU' };
    const normalize = (items, source) => items.flatMap((item) => {
      const lat = source === 'markers' ? item.lat : item.point?.lat;
      const lon = source === 'markers' ? item.lon : item.point?.lon;
      if (!Number.isFinite(lat) || !Number.isFinite(lon) || typeof item.name !== 'string' || !item.name.trim() || !item.id) return [];
      const coordinates = [lat, lon];
      const distance = distanceMeters(cafeCenter, coordinates);
      return distance <= radius ? [{ id: item.id, name: item.name.trim(), coordinates, distance }] : [];
    }).sort((a, b) => a.distance - b.distance);
    async function findCafes() {
      try {
        try {
          const markers = await catalogRequest('/3.0/markers', { ...parameters, fields: 'items.name' }, apiKey, controller.signal);
          if (controller.signal.aborted) return;
          const cafes = normalize(markers, 'markers');
          if (cafes.length) { onCafesRef.current(cafes, null); return; }
        } catch (error) {
          if (controller.signal.aborted) return;
        }
        const places = await catalogRequest('/3.0/items', { ...parameters, fields: 'items.point' }, apiKey, controller.signal);
        if (controller.signal.aborted) return;
        const cafes = normalize(places, 'places');
        if (places.length && !cafes.length) throw new Error('CATALOG_NO_COORDINATES');
        onCafesRef.current(cafes, null);
      } catch (error) {
        if (controller.signal.aborted) return;
        onCafesRef.current([], error.message === 'CATALOG_ACCESS_DENIED' ? 'Ключ 2ГИС не разрешает поиск заведений через Places API.'
          : error.message === 'CATALOG_RATE_LIMITED' ? 'Достигнут лимит запросов к поиску 2ГИС.'
            : error.message === 'CATALOG_NO_COORDINATES' ? '2ГИС нашёл заведения, но не передал их координаты.'
              : 'Не удалось связаться с поиском 2ГИС. Попробуйте ещё раз.');
      }
    }
    findCafes();
    return () => controller.abort();
  }, [radius, cafeCenter?.join(','), apiKey]);

  return (
    <section className="workspace-map" aria-label="Карта маршрута 2ГИС">
      <div className="workspace-map-top"><span>{lunchNavigation ? 'Пешком до кафе' : 'Карта маршрута'}</span><span>2ГИС</span></div>
      <div className="workspace-map-canvas">
        <div ref={container} className="workspace-2gis-map" aria-hidden={state !== 'ready'} />
        {state !== 'ready' && <div className="workspace-map-state">{state === 'no-key' ? 'Для карты нужен ключ 2ГИС. Расписание доступно ниже.' : state === 'error' ? 'Карта не загрузилась. Расписание доступно ниже.' : 'Загружаем карту…'}</div>}
        {state === 'ready' && !lunchNavigation && ['request-error', 'unavailable', 'missing-points'].includes(routeState) && <div className="workspace-route-error" role="status">{routeState === 'request-error' ? 'Не удалось получить маршрут 2ГИС' : routeState === 'missing-points' ? 'Для маршрута нужны минимум две точки' : '2ГИС не построил маршрут'}{routeIssue && <small>{routeIssue}</small>}</div>}
        {pickMode && state === 'ready' && <div className="workspace-map-pick"><span>Нажмите на карте, чтобы указать {pickMode === 'start' ? 'старт' : 'финиш'}</span><button type="button" onClick={onCancelPick}>Отмена</button></div>}
      </div>
      <div className="workspace-map-caption"><span>{lunchNavigation ? walkingState === 'ready' ? `Пеший путь до «${lunchNavigation.cafe.name}» построен 2ГИС` : walkingState === 'error' ? 'Пеший путь на карте недоступен. Проверьте его в 2ГИС.' : 'Строим пеший путь до кафе…' : routeState === 'ready' ? 'Путь построен 2ГИС · расписание демонстрационное' : routeState === 'loading' ? 'Строим маршрут по улицам…' : routeState === 'missing-points' ? 'Для маршрута нужны минимум две точки' : routeState === 'request-error' ? 'Точки показаны; маршрут по улицам пока недоступен' : '2ГИС не смог построить путь через эти точки'}</span></div>
      {lunchNavigation && validPoint(lunchNavigation.origin) && validPoint(lunchNavigation.cafe.coordinates) && <a className="workspace-open-2gis is-walking" href={twoGisRouteUrl([lunchNavigation.origin, lunchNavigation.cafe.coordinates], 'walk')} target="_blank" rel="noopener noreferrer" onClick={(event) => {
        if (window.WebApp?.openLink) { event.preventDefault(); window.WebApp.openLink(event.currentTarget.href); }
      }}>{walkingState === 'ready' ? 'Идти в 2ГИС' : 'Проверить путь в 2ГИС'} <span aria-hidden="true">↗</span></a>}
      {!lunchNavigation && routeLink && routeState === 'ready' && <a className="workspace-open-2gis" href={routeLink} target="_blank" rel="noopener noreferrer" onClick={(event) => {
        if (window.WebApp?.openLink) { event.preventDefault(); window.WebApp.openLink(routeLink); }
      }}>Открыть в 2ГИС <span aria-hidden="true">↗</span></a>}
    </section>
  );
}
