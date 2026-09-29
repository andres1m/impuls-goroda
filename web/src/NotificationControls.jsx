import React, { useEffect, useRef, useState } from 'react';
import { createNotificationAttempt, loadNotificationPreference, sendNotificationPreference } from './notifications.js';
import { loadOwnerRoute } from './route.js';

export default function NotificationControls({ route, apiBaseUrl, accessToken, disabled, onBlockingChange, onRouteUpdated }) {
  const [value, setValue] = useState(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [retry, setRetry] = useState(false);
  const [conflict, setConflict] = useState(false);
  const attempt = useRef(null);
  const request = useRef(null);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    request.current = controller;
    attempt.current = null;
    setValue(null); setBusy(true); setMessage(''); setRetry(false); setConflict(false);
    loadNotificationPreference(apiBaseUrl, accessToken, route.route_id, fetch, controller.signal)
      .then((result) => { if (active) setValue(result); })
      .catch(() => { if (active) setMessage('Не удалось загрузить настройку.'); })
      .finally(() => { if (active) { request.current = null; setBusy(false); } });
    return () => { active = false; request.current?.abort(); };
  }, [apiBaseUrl, accessToken, route.route_id, route.revision]);

  useEffect(() => () => onBlockingChange(false), [onBlockingChange]);

  async function perform(enabled) {
    if (request.current && !request.current.signal.aborted || disabled) return;
    const controller = new AbortController();
    request.current = controller;
    setBusy(true); setMessage('');
    const saving = enabled !== undefined || Boolean(attempt.current);
    onBlockingChange(true);
    try {
      if (saving) {
        if (!attempt.current) attempt.current = createNotificationAttempt(route, value.preference, enabled);
        const result = await sendNotificationPreference(apiBaseUrl, accessToken, attempt.current, fetch, controller.signal);
        if (controller.signal.aborted) return;
        setValue(result); attempt.current = null; setRetry(false); setConflict(false);
        setMessage(result.preference.enabled ? 'Уведомления включены.' : 'Уведомления выключены.');
      } else {
        const updated = conflict ? await loadOwnerRoute(apiBaseUrl, accessToken, route.route_id, fetch, controller.signal) : null;
        const result = await loadNotificationPreference(apiBaseUrl, accessToken, route.route_id, fetch, controller.signal);
        if (controller.signal.aborted) return;
        if (updated && result.revision !== updated.revision) throw new Error('Route changed during refresh');
        setValue(result); setConflict(false);
        if (updated) onRouteUpdated(updated);
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      if (saving && error.status === 409) {
        attempt.current = null; setRetry(false); setConflict(true);
        setMessage('Настройка или маршрут изменились. Обновите данные перед новым действием.');
      } else if (saving && [400, 401, 403, 404].includes(error.status)) {
        attempt.current = null; setRetry(false);
        setMessage('Настройка не сохранена. Обновите данные или откройте маршрут заново.');
      } else {
        setRetry(Boolean(attempt.current));
        setMessage(saving ? 'Результат сохранения неизвестен. Повторите тот же запрос.' : 'Не удалось загрузить настройку.');
      }
    } finally {
      if (!controller.signal.aborted) {
        request.current = null; setBusy(false); onBlockingChange(Boolean(attempt.current));
      }
    }
  }

  const platform = value?.preference.platform_state;
  return <section className="owner-route-actions notification-controls" aria-labelledby="notification-title" aria-busy={busy}>
    <h2 id="notification-title">Уведомления в MAX</h2>
    <label className="notification-switch">
      <span><strong>Уведомлять об отменах</strong><small>Если посещение в этом маршруте отменится</small></span>
      <input type="checkbox" role="switch" checked={value?.preference.enabled === true} disabled={disabled || busy || !value || retry || conflict}
        onChange={(event) => perform(event.target.checked)} aria-describedby="notification-status" aria-label="Уведомлять об отменах" />
    </label>
    <p id="notification-status" className="notification-hint" role="status">{busy ? 'Загружаем…' : message || (!value ? 'Настройка пока неизвестна.' : value.preference.enabled ? 'Включены для этого маршрута.' : 'Выключены.')}</p>
    {platform === 'stopped' && <p className="notification-hint">Откройте диалог с ботом и запустите его снова. Сейчас отправка ограничена.</p>}
    {platform === 'muted' && <p className="notification-hint">Уведомления диалога отключены в MAX. Включите их, чтобы получать сообщения.</p>}
    {platform === 'unknown' && value?.preference.enabled && <p className="notification-hint">Доступность отправки пока не подтверждена.</p>}
    {(retry || conflict || !value && !busy || message.startsWith('Настройка не сохранена')) &&
      <button className="scenario-option" disabled={disabled || busy} onClick={() => perform()}>{retry ? 'Повторить сохранение' : 'Обновить настройку'}</button>}
  </section>;
}
