export const archetypeTitles = { urban_avantgarde: 'Современный город', history_heritage: 'История и культура', action_social: 'Движение и польза' };
export const categoryTitles = { culture: 'Культура', sport: 'Спорт', volunteer: 'Волонтёрство', walk: 'Прогулка', tourism: 'Туризм', gastro: 'Гастрономия' };
export const travelTitles = { walk: 'Пешком', transit: 'Общественный транспорт', car: 'На автомобиле' };
export const verificationTitles = { verified: 'Подтверждён', estimated: 'Приблизительная оценка', unknown: 'Путь не проверен', unavailable: 'Путь недоступен' };

export function coordinate(value) {
  return value && Number.isFinite(value.latitude) && Math.abs(value.latitude) <= 90 && Number.isFinite(value.longitude) && Math.abs(value.longitude) <= 180
    ? [value.latitude, value.longitude] : null;
}

export function projectRoute(route) {
  const plan = route.plan;
  const steps = [...plan.steps].sort((a, b) => a.position - b.position);
  const legs = [...(plan.legs || [])].sort((a, b) => a.position - b.position);
  const segments = legs.flatMap((leg) => {
    if (!Array.isArray(leg.geometry) || leg.geometry.length < 2) return [];
    const points = leg.geometry.map(coordinate);
    return points.every(Boolean) ? [{ leg, points }] : [];
  });
  const locations = new Map();
  segments.forEach(({ leg, points }) => { if (leg.to_kind === 'visit' && leg.to_visit_id) locations.set(leg.to_visit_id, points.at(-1)); });
  segments.forEach(({ leg, points }) => { if (leg.from_kind === 'visit' && leg.from_visit_id && !locations.has(leg.from_visit_id)) locations.set(leg.from_visit_id, points[0]); });
  const execution = new Map((route.execution || []).map((item) => [item.visit_id, item]));
  const visits = steps.filter((step) => step.kind === 'visit').map((step, index) => ({
    id: step.visit_id, number: index + 1, title: step.catalog?.title || 'Название не указано',
    point: locations.get(step.visit_id), completed: execution.get(step.visit_id)?.status === 'completed',
  }));
  return { steps, legs, segments, visits, execution, origin: coordinate(plan.origin), destination: coordinate(plan.destination) };
}
