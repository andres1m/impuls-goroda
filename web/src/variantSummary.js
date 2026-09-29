import { archetypeTitles } from './routeProjection.js';

const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
function invalid() { throw new Error('Сохранённый вариант содержит неполные данные.'); }
function money(value) {
  if (!object(value) || typeof value.amount_minor !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value.amount_minor) ||
      typeof value.currency !== 'string' || !/^[A-Z]{3}$/.test(value.currency)) invalid();
  const minor = BigInt(value.amount_minor);
  if (minor > 9223372036854775807n) invalid();
  if (value.currency !== 'RUB') return `Сумма в ${value.currency} — в маршруте`;
  return `${minor / 100n},${String(minor % 100n).padStart(2, '0')} ₽`;
}
function timestamp(value) {
  if (typeof value !== 'string' || !Number.isFinite(Date.parse(value))) invalid();
  return Date.parse(value);
}

export function summarizeVariant(route) {
  const plan = route?.plan;
  if (!object(plan) || !['READY', 'PARTIAL'].includes(plan.result) || !Array.isArray(plan.steps) ||
      !Array.isArray(plan.legs) || !Array.isArray(plan.warnings) || !Array.isArray(route.issues) ||
      !object(plan.cost) || !Array.isArray(plan.cost.unknown_components)) invalid();
  const start = timestamp(plan.start_at), end = timestamp(plan.end_at);
  if (end <= start || typeof plan.timezone !== 'string') invalid();
  const date = new Intl.DateTimeFormat('ru-RU', { timeZone: plan.timezone, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  const visits = [];
  const seen = new Map();
  const modes = new Set();
  let travelSeconds = 0, walkSeconds = 0, uncertainLegs = 0, pauses = 0;
  const issues = route.issues.filter((issue) => {
    if (!object(issue) || !['open', 'acknowledged', 'resolved'].includes(issue.state)) invalid();
    return issue.state !== 'resolved';
  });
  for (const step of [...plan.steps].sort((a, b) => a.position - b.position)) {
    if (!object(step) || !Number.isSafeInteger(step.position) || !['visit', 'free_time'].includes(step.kind)) invalid();
    if (step.kind === 'free_time') { pauses++; continue; }
    const catalog = step.catalog;
    if (!object(catalog) || typeof catalog.title !== 'string' || !catalog.title.trim() || !['live', 'prepared', 'synthetic'].includes(catalog.data_mode)) invalid();
    const identity = [catalog.place_id, catalog.event_id, catalog.session_id].map((id) => id || '').join('/');
    const count = (seen.get(identity) || 0) + 1; seen.set(identity, count);
    visits.push({ title: catalog.title, identity: identity === '//' ? null : `${identity}#${count}`, dataMode: catalog.data_mode });
  }
  if (!visits.length) invalid();
  for (const leg of plan.legs) {
    if (!object(leg) || !['walk', 'transit', 'car'].includes(leg.mode) || !['verified', 'estimated', 'unknown', 'unavailable'].includes(leg.verification)) invalid();
    const seconds = (timestamp(leg.arrival_at) - timestamp(leg.departure_at)) / 1000;
    if (seconds < 0) invalid();
    travelSeconds += seconds; if (leg.mode === 'walk') walkSeconds += seconds;
    modes.add(leg.mode); if (leg.verification !== 'verified') uncertainLegs++;
  }
  return {
    title: archetypeTitles[plan.archetype_id] || 'Маршрут', window: `${date.format(start)} — ${date.format(end)}`,
    visits, pauses, modes: [...modes], travelMinutes: Math.ceil(travelSeconds / 60), walkMinutes: Math.ceil(walkSeconds / 60), uncertainLegs,
    personalCost: money(plan.cost.known_personal), transportCost: money(plan.cost.known_transport), unknownCostCount: plan.cost.unknown_components.length,
    warnings: plan.warnings.map((item) => { if (!object(item) || typeof item.message !== 'string' || !item.message.trim()) invalid(); return item.message; }),
    issueCount: issues.length, partial: plan.result === 'PARTIAL', dataModes: [...new Set(visits.map((visit) => visit.dataMode))],
  };
}

export function variantDifference(summary, others) {
  if (!others.length || [...summary.visits, ...others.flatMap((value) => value.visits)].some((visit) => !visit.identity)) return null;
  const composition = (value) => value.visits.map((visit) => visit.identity).sort().join('|');
  if (others.every((value) => composition(value) === composition(summary))) return 'Посещения совпадают. Сравните порядок, время и стоимость.';
  const otherIDs = new Set(others.flatMap((value) => value.visits.map((visit) => visit.identity)));
  const exclusive = summary.visits.filter((visit) => !otherIDs.has(visit.identity));
  return exclusive.length ? `Только здесь среди загруженных вариантов: ${exclusive.map((visit) => visit.title).join(', ')}.` : 'Часть посещений совпадает с другими вариантами. Сравните последовательность ниже.';
}
