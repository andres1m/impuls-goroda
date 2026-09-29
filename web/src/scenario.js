import { GatewayError, parseOptimizeResponse } from './optimize.js';

const scenarioIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const presets = new Set(['vibe', 'mood', 'culture', 'energy', 'balance', 'benefit']);
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);

export function validScenarioVersion(value) {
  return typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;
}

function invalidResponse() { return new Error('Invalid scenario response'); }

export function parseScenarioResponse(body, expectedID) {
  if (!object(body) || typeof body.request_id !== 'string' || !body.request_id || !('scenario' in body)) throw invalidResponse();
  if (body.scenario === null) {
    if (expectedID) throw invalidResponse();
    return null;
  }
  const value = body.scenario;
  if (!object(value) || !scenarioIDPattern.test(value.scenario_id) || !validScenarioVersion(value.version) ||
      expectedID && value.scenario_id.toLowerCase() !== expectedID.toLowerCase() ||
      !['draft', 'completed'].includes(value.status) || !object(value.input) ||
      typeof value.updated_at !== 'string' || !Number.isFinite(Date.parse(value.updated_at)) ||
      !['preset', 'custom'].includes(value.source) ||
      value.source === 'preset' && !presets.has(value.preset_id) ||
      value.source === 'custom' && (typeof value.source_text !== 'string' || !value.source_text.trim()) ||
      value.pending_extraction !== undefined && !object(value.pending_extraction)) throw invalidResponse();
  const outcome = value.outcome;
  if (value.status === 'completed' && (!outcome || value.pending_extraction !== undefined)) throw invalidResponse();
  if (outcome !== undefined) {
    if (!object(outcome) || !['READY', 'PARTIAL', 'NO_FEASIBLE_ROUTE', 'CONFLICT'].includes(outcome.status) ||
        !Array.isArray(outcome.route_ids) || outcome.route_ids.length > 3 ||
        !outcome.route_ids.every((id) => typeof id === 'string' && uuidPattern.test(id)) ||
        new Set(outcome.route_ids.map((id) => id.toLowerCase())).size !== outcome.route_ids.length ||
        !Array.isArray(outcome.warnings) || !Array.isArray(outcome.conflicts) ||
        !['live', 'prepared', 'synthetic'].includes(outcome.data_mode)) throw invalidResponse();
    const success = ['READY', 'PARTIAL'].includes(outcome.status);
    if (success !== (value.status === 'completed') || success && (!outcome.route_ids.length || outcome.conflicts.length) ||
        !success && outcome.route_ids.length || outcome.status === 'CONFLICT' && !outcome.conflicts.length) throw invalidResponse();
  }
  return value;
}

async function request(apiBaseUrl, path, accessToken, options, fetcher, signal) {
  if (typeof accessToken !== 'string' || !accessToken) throw new Error('Authorization is required');
  const response = await fetcher(`${apiBaseUrl}${path}`, {
    ...options, headers: { ...options.headers, Authorization: `Bearer ${accessToken}` },
    credentials: 'omit', cache: 'no-store', signal,
  });
  let body;
  try { body = await response.json(); } catch { throw invalidResponse(); }
  if (!response.ok) {
    if (!object(body) || typeof body.code !== 'string' || !/^[A-Z][A-Z0-9_]*$/.test(body.code) ||
        typeof body.message !== 'string' || !body.message || typeof body.request_id !== 'string' || !body.request_id ||
        typeof body.retryable !== 'boolean' ||
        body.current_version !== undefined && !validScenarioVersion(body.current_version) ||
        body.code === 'SCENARIO_VERSION_CONFLICT' && !validScenarioVersion(body.current_version)) throw invalidResponse();
    throw new GatewayError(response.status, body);
  }
  if (response.status !== 200) throw invalidResponse();
  return body;
}

export async function loadScenario(apiBaseUrl, accessToken, scenarioID = null, fetcher = fetch, signal) {
  if (scenarioID !== null && (typeof scenarioID !== 'string' || !scenarioIDPattern.test(scenarioID))) throw new Error('Invalid scenario identifier');
  const path = scenarioID === null ? '/api/v1/me/scenario' : `/api/v1/scenarios/${scenarioID.toLowerCase()}`;
  return parseScenarioResponse(await request(apiBaseUrl, path, accessToken, { method: 'GET' }, fetcher, signal), scenarioID);
}

export function createCompletionAttempt(scenario, confirmedInput, idempotencyKey = crypto.randomUUID()) {
  if (!object(scenario) || !scenarioIDPattern.test(scenario.scenario_id) || !validScenarioVersion(scenario.version) ||
      scenario.status !== 'draft' || !object(confirmedInput) || typeof idempotencyKey !== 'string' || !uuidPattern.test(idempotencyKey)) {
    throw new Error('Invalid scenario completion');
  }
  return Object.freeze({
    scenarioID: scenario.scenario_id.toLowerCase(), key: idempotencyKey,
    body: JSON.stringify({ expected_version: scenario.version, input: confirmedInput }),
  });
}

export async function completeScenario(apiBaseUrl, accessToken, attempt, fetcher = fetch, signal) {
  if (!object(attempt) || !scenarioIDPattern.test(attempt.scenarioID) || !uuidPattern.test(attempt.key) || typeof attempt.body !== 'string') {
    throw new Error('Invalid completion attempt');
  }
  const body = await request(apiBaseUrl, `/api/v1/scenarios/${attempt.scenarioID}/complete`, accessToken, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': attempt.key }, body: attempt.body,
  }, fetcher, signal);
  return parseOptimizeResponse(body);
}
