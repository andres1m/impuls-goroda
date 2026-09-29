const statuses = new Set(['available', 'degraded', 'unavailable']);
const capabilities = ['read_saved_routes', 'mutate_routes', 'optimize', 'catalog_updates'];
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);

export async function loadAvailability(apiBaseUrl, signal, fetcher = fetch) {
  const response = await fetcher(`${apiBaseUrl}/api/v1/health/ready`, { method: 'GET', credentials: 'omit', cache: 'no-store', signal });
  if (![200, 503].includes(response.status)) throw new Error('Availability status was not received');
  const value = await response.json();
  if (!object(value) || !statuses.has(value.status) || typeof value.checked_at !== 'string' || !Number.isFinite(Date.parse(value.checked_at)) ||
      typeof value.request_id !== 'string' || !value.request_id || !object(value.capabilities) ||
      (response.status === 503) !== (value.status === 'unavailable')) throw new Error('Invalid availability status');
  for (const name of capabilities) {
    const capability = value.capabilities[name];
    if (!object(capability) || !statuses.has(capability.status) ||
        capability.reason !== undefined && (typeof capability.reason !== 'string' || !/^[A-Z][A-Z0-9_]*$/.test(capability.reason))) throw new Error('Invalid availability capability');
  }
  return value;
}

export function availabilityMessages(value) {
  const c = value.capabilities, messages = [];
  if (c.read_saved_routes.status === 'unavailable') messages.push('Сервис сообщил о проблеме доступа к сохранённым маршрутам. Уже открытый план остаётся на экране.');
  if (c.optimize.status === 'unavailable') messages.push('Расчёт новых маршрутов сейчас недоступен.');
  else if (c.optimize.status === 'degraded') messages.push('Готовность расчёта пока не подтверждена. При ошибке можно повторить запрос, сохранив условия.');
  if (c.mutate_routes.status === 'unavailable') messages.push('Сервис сообщил, что сохранение изменений сейчас недоступно.');
  if (c.catalog_updates.status === 'unavailable') messages.push('Обновления об отменах и изменениях событий сейчас недоступны. Перед выходом проверьте актуальность посещений.');
  else if (c.catalog_updates.status === 'degraded') messages.push('Обновления событий работают с ограничениями. Перед выходом проверьте актуальность посещений.');
  return messages;
}
