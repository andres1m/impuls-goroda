const scenarioPattern = /^scenario_([0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$/i;
const ownerRoutePattern = /^route_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i;

export function parseLaunchPayload(payload) {
  if (payload === undefined || payload === null || payload === '') return { kind: 'default' };
  if (typeof payload !== 'string' || payload.length > 512 || !/^[A-Za-z0-9_-]+$/.test(payload)) {
    throw new Error('Invalid launch payload');
  }
  if (payload.startsWith('scenario_')) {
    const match = scenarioPattern.exec(payload);
    if (!match) throw new Error('Invalid scenario link');
    return { kind: 'scenario', scenarioID: match[1].toLowerCase() };
  }
  if (payload.startsWith('demo_')) return { kind: 'demo', payload };
  if (payload.startsWith('route_') && payload.length === 42) {
    const match = ownerRoutePattern.exec(payload);
    if (!match || match[1] === '00000000-0000-0000-0000-000000000000') throw new Error('Invalid owner route link');
    return { kind: 'owner', routeID: match[1].toLowerCase() };
  }
  return { kind: 'shared', shareToken: payload };
}

export async function readLaunchContext(webApp = window.WebApp, search = window.location.search) {
  const parameters = new URLSearchParams(search).getAll('WebAppStartParam');
  if (parameters.length > 1) throw new Error('Ambiguous launch payload');
  const launch = parseLaunchPayload(webApp?.initDataUnsafe?.start_param || parameters[0]);
  let entryPoint = 'default';
  if (typeof webApp?.getLaunchContext === 'function') {
    try {
      const context = await webApp.getLaunchContext();
      if (context?.entryPoint === 'tabbar') entryPoint = 'tabbar';
    } catch {
      // Older clients can expose a bridge method that they do not support.
    }
  }
  return { ...launch, entryPoint };
}
