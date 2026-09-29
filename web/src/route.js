export class RouteRequestError extends Error {
  constructor(status, code, retryable) {
    super(code);
    this.name = 'RouteRequestError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

async function readJson(response) {
  try {
    return await response.json();
  } catch {
    throw new RouteRequestError(response.status, 'INVALID_RESPONSE', false);
  }
}

async function get(apiBaseUrl, path, accessToken, fetcher, signal) {
  const response = await fetcher(`${apiBaseUrl}${path}`, {
    method: 'GET',
    headers: { Authorization: `Bearer ${accessToken}` },
    credentials: 'omit',
    cache: 'no-store',
    signal,
  });
  const body = await readJson(response);
  if (!response.ok) {
    throw new RouteRequestError(response.status, body.code || 'REQUEST_FAILED', body.retryable === true);
  }
  return body;
}

export async function loadSelectedRoute(apiBaseUrl, accessToken, fetcher = fetch, signal) {
  const context = await get(apiBaseUrl, '/api/v1/me/context', accessToken, fetcher, signal);
  if (!context || typeof context !== 'object' || !('confirmed_input' in context)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  if (!context.selected_route_id) return null;
  return loadOwnerRoute(apiBaseUrl, accessToken, context.selected_route_id, fetcher, signal);
}

export async function loadOwnerRoute(apiBaseUrl, accessToken, id, fetcher = fetch, signal) {
  if (!/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(id)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  const body = await get(apiBaseUrl, `/api/v1/routes/${id}`, accessToken, fetcher, signal);
  if (!body?.route?.plan || typeof body.route.route_id !== 'string' || body.route.route_id.toLowerCase() !== id.toLowerCase() || !Array.isArray(body.route.plan.steps) ||
      typeof body.route.revision !== 'string' || !/^[1-9][0-9]{0,18}$/.test(body.route.revision) || BigInt(body.route.revision) > 9223372036854775807n ||
      !['draft', 'saved'].includes(body.route.lifecycle)) {
    throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
  }
  const proposal = body.route.pending_proposal;
  if (proposal !== undefined) {
    const revision = (value) => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;
    const validID = (value) => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
    if (!proposal || !validID(proposal.proposal_id) || proposal.state !== 'pending' || !['delay', 'delete'].includes(proposal.reason) ||
        proposal.base_revision !== body.route.revision || !revision(proposal.base_catalog_revision) || !Number.isFinite(Date.parse(proposal.created_at)) ||
        !['READY', 'PARTIAL'].includes(proposal.candidate?.result) || !Array.isArray(proposal.candidate?.steps) || !Array.isArray(proposal.candidate?.legs) ||
        proposal.candidate.catalog_revision !== proposal.base_catalog_revision || proposal.candidate.timezone !== body.route.plan.timezone ||
        Date.parse(proposal.candidate.start_at) !== Date.parse(body.route.plan.start_at) || Date.parse(proposal.candidate.end_at) !== Date.parse(body.route.plan.end_at) ||
        !Array.isArray(proposal.conflicts) || proposal.conflicts.length || !Array.isArray(proposal.changes) ||
        proposal.reason === 'delay' && !Number.isFinite(Date.parse(proposal.effective_start_at))) {
      throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
    }
    const beforeIDs = new Set(body.route.plan.steps.map((step) => step.visit_id));
    const afterIDs = new Set(proposal.candidate.steps.map((step) => step.visit_id));
    if (afterIDs.size !== proposal.candidate.steps.length || proposal.candidate.steps.some((step) => !validID(step.visit_id)) ||
        proposal.changes.some((change) => !change || typeof change.message !== 'string' || !change.message.trim() ||
          !['kept', 'removed', 'replaced', 'time_shifted', 'cost_changed', 'participation_action', 'verification_changed'].includes(change.kind) ||
          !['route', 'visit', 'leg'].includes(change.scope) || change.before_visit_id !== undefined && !beforeIDs.has(change.before_visit_id) ||
          change.after_visit_id !== undefined && !afterIDs.has(change.after_visit_id))) {
      throw new RouteRequestError(200, 'INVALID_RESPONSE', false);
    }
  }
  return body.route;
}
