import React, { useEffect, useRef, useState } from 'react';
import { fitPoints, loadMapGL } from './TwoGisRouteMap.jsx';

export default function ServerRouteMap({ apiKey, projection, onSelect }) {
  const container = useRef(null);
  const select = useRef(onSelect);
  const [state, setState] = useState('loading');
  const [attempt, setAttempt] = useState(0);
  select.current = onSelect;
  useEffect(() => {
    if (!apiKey) { setState('no-key'); return; }
    let active = true;
    let map;
    const objects = [];
    setState('loading');
    const points = [...projection.segments.flatMap((segment) => segment.points), ...projection.visits.flatMap((visit) => visit.point ? [visit.point] : []), ...[projection.origin, projection.destination].filter(Boolean)];
    if (!points.length) { setState('empty'); return; }
    loadMapGL().then((mapgl) => {
      if (!active) return;
      map = new mapgl.Map(container.current, { center: [points[0][1], points[0][0]], zoom: 13, key: apiKey, enableTrackResize: true, zoomControl: 'centerLeft' });
      map.on('error', () => { if (active) setState('error'); });
      projection.segments.forEach(({ leg, points: geometry }) => {
        if (leg.verification === 'unavailable') return;
        objects.push(new mapgl.Polyline(map, { coordinates: geometry.map(([lat, lon]) => [lon, lat]), width: 5, color: leg.verification === 'verified' ? '#087af5' : '#8799b3' }));
      });
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
      setState('ready');
    }).catch(() => { if (active) setState('error'); });
    return () => { active = false; objects.forEach((object) => object.destroy()); map?.destroy(); };
  }, [apiKey, projection, attempt]);

  const missing = projection.visits.filter((visit) => !visit.point).length;
  return <section className="workspace-map-column workspace-map" aria-label="Карта маршрута">
    <div className="workspace-map-top"><span>Карта маршрута</span><span>2ГИС</span></div>
    <div className="workspace-map-canvas"><div ref={container} className="workspace-2gis-map" aria-hidden={state !== 'ready'} />
      {state !== 'ready' && <div className="workspace-map-state server-map-state" role="status"><p>{state === 'loading' ? 'Загружаем карту…' : state === 'no-key' ? 'Карта не настроена. Расписание доступно ниже.' : state === 'empty' ? 'В маршруте нет координат для карты.' : 'Не удалось загрузить карту. Расписание доступно ниже.'}</p>{state === 'error' && <button className="scenario-option" onClick={() => setAttempt((value) => value + 1)}>Повторить</button>}</div>}
    </div>
    <div className="workspace-map-caption"><span>Синий — подтверждённый путь; серый — непроверенный.{missing ? ` Без координат: ${missing} посещений.` : ''}{projection.segments.length < projection.legs.length ? ' Часть переходов без геометрии.' : ''}</span></div>
  </section>;
}
