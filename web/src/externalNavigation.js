const modes = { walk: 'pedestrian', transit: 'bus', car: 'car' };

function validPoint(point) {
  return Array.isArray(point) && point.length === 2 && point.every(Number.isFinite)
    && Math.abs(point[0]) <= 90 && Math.abs(point[1]) <= 180;
}

export function externalLegLink(leg, points) {
  if (leg.verification === 'unavailable' || !modes[leg.mode] || !Array.isArray(points)
    || points.length < 2 || !points.every(validPoint)) return null;
  const endpoints = [points[0], points.at(-1)].map(([lat, lon]) => `${lon},${lat};`);
  return `https://2gis.ru/directions/tab/${modes[leg.mode]}/points/${endpoints.join('|')}`;
}

export function openExternalNavigation(event) {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const bridge = window.WebApp;
  if (typeof bridge?.openLink !== 'function') return;
  try {
    bridge.openLink(event.currentTarget.href);
    event.preventDefault();
  } catch {
    // Keep the browser link available when the bridge rejects the click.
  }
}
