import React, { useEffect, useState } from 'react';
import { availabilityMessages, loadAvailability } from './availability.js';

export default function ServiceAvailability({ apiBaseUrl }) {
  const [state, setState] = useState({ kind: 'checking' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let ignore = false;
    const timeout = setTimeout(() => controller.abort(), 5000);
    setState((old) => ({ kind: 'checking', value: old.apiBaseUrl === apiBaseUrl ? old.value : null, apiBaseUrl }));
    loadAvailability(apiBaseUrl, controller.signal).then((value) => {
      if (!ignore) setState({ kind: 'ready', value, apiBaseUrl });
    }).catch(() => {
      if (!ignore) setState({ kind: 'unknown', apiBaseUrl });
    }).finally(() => clearTimeout(timeout));
    return () => { ignore = true; clearTimeout(timeout); controller.abort(); };
  }, [apiBaseUrl, attempt]);
  const visible = state.apiBaseUrl === apiBaseUrl ? state : { kind: 'checking' };
  if (visible.kind === 'checking' && !visible.value) return null;
  const messages = visible.value ? availabilityMessages(visible.value) : ['Не удалось получить статус сервиса. Это не означает, что сохранённый маршрут недоступен.'];
  if (visible.kind === 'ready' && !messages.length) return null;
  return <aside className="service-availability" aria-label="Доступность сервиса" aria-busy={visible.kind === 'checking'}>
    <div role="status"><strong>{visible.kind === 'unknown' ? 'Статус сервиса неизвестен' : 'Сервис работает с ограничениями'}</strong>
      {messages.length > 0 && <p>{messages[0]}</p>}
      {messages.length > 1 && <details><summary>Другие ограничения</summary>{messages.slice(1).map((message, index) => <p key={index}>{message}</p>)}</details>}
      {visible.kind === 'checking' && <p>Обновляем статус…</p>}
    </div>
    <button type="button" disabled={visible.kind === 'checking'} onClick={() => setAttempt((old) => old + 1)}>Обновить статус</button>
  </aside>;
}
