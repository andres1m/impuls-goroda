import React from 'react';
import { externalLegLink, openExternalNavigation } from './externalNavigation.js';
import { travelTitles } from './routeProjection.js';

export default function RouteNavigation({ projection }) {
  if (!projection.legs.length) return null;
  const visits = new Map(projection.visits.map((visit) => [visit.id, visit]));
  function endpoint(kind, id) {
    if (kind === 'origin') return 'Старт';
    if (kind === 'destination') return 'Финиш';
    const visit = visits.get(id);
    return visit ? `${visit.number}. ${visit.title}` : 'Точка перехода';
  }
  return <details className="server-navigation">
    <summary>Как добраться · 2ГИС</summary>
    <p>Выберите переход. 2ГИС рассчитает свой путь; расписание дня здесь не изменится.</p>
    <ol>
      {projection.legs.map((leg) => {
        const points = projection.segments.find((segment) => segment.leg === leg)?.points;
        const link = externalLegLink(leg, points);
        const source = leg.from_kind === 'origin' ? projection.origin : visits.get(leg.from_visit_id)?.point;
        const sourceMatches = source && points && source[0] === points[0][0] && source[1] === points[0][1];
        const label = `${sourceMatches ? endpoint(leg.from_kind, leg.from_visit_id) : 'Начало перехода'} → ${endpoint(leg.to_kind, leg.to_visit_id)}`;
        return <li key={leg.position}>
          <strong>{label}</strong>
          <span>{travelTitles[leg.mode] || 'Способ передвижения не указан'}</span>
          {link ? <a href={link} target="_blank" rel="noopener noreferrer" onClick={openExternalNavigation} aria-label={`Открыть в 2ГИС: ${label}`}>Открыть в 2ГИС</a>
            : <small>{leg.verification === 'unavailable' ? 'Переход недоступен в сохранённом плане.' : !['walk', 'transit', 'car'].includes(leg.mode) ? 'Этот способ передвижения не поддержан ссылкой.' : 'В плане нет координат этого перехода.'}</small>}
        </li>;
      })}
    </ol>
  </details>;
}
