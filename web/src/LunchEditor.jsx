import React, { useEffect, useState } from 'react';
import { venueForLunch } from './lunchCommands.js';

function label(step) { return step.catalog?.title || step.external_venue?.title || 'Посещение'; }

export default function LunchEditor({ route, selected, disabled, pending, onSubmit, onClear }) {
  const visits = route.plan.steps.filter((step) => step.kind === 'visit');
  const lunches = route.plan.steps.filter((step) => Boolean(step.lunch));
  const [action, setAction] = useState('add');
  const [lunchID, setLunchID] = useState(lunches[0]?.visit_id || '');
  const [afterID, setAfterID] = useState(visits[0]?.visit_id || '');
  const [duration, setDuration] = useState(2700);
  const [acknowledged, setAcknowledged] = useState(false);
  const [freeTime, setFreeTime] = useState(false);
  useEffect(() => { setLunchID(lunches[0]?.visit_id || ''); setAfterID(visits[0]?.visit_id || ''); setAction('add'); setAcknowledged(false); setFreeTime(false); }, [route.route_id, route.revision]);
  const chosen = lunches.find((step) => step.visit_id === lunchID);
  useEffect(() => {
    if (action !== 'add' && chosen) { setAfterID(chosen.lunch.after_visit_id); setDuration(chosen.lunch.duration_seconds); }
  }, [action, lunchID]);
  if (route.lifecycle !== 'saved') return <section className="owner-route-actions lunch-editor"><p>Сохраните маршрут, чтобы добавить обед в расписание.</p></section>;
  function submit(event) {
    event.preventDefault();
    if (disabled || pending) return;
    const input = { action };
    if (action !== 'add') { input.lunch_id = lunchID; input.acknowledge_external_commitment = acknowledged; }
    if (action !== 'remove') {
      input.placement = { after_visit_id: afterID, duration_seconds: Number(duration) };
      const venue = venueForLunch(action === 'update' ? chosen : null, selected, freeTime);
      if (venue) input.venue = venue;
    }
    onSubmit(input);
  }
  return <section className="owner-route-actions lunch-editor" aria-label="Обед в маршруте">
    <h2>Обед в маршруте</h2>
    <p>Запросите пересчёт, сравните расписания и подтвердите изменение. При переносе выбранное кафе сохраняется.</p>
    {selected && <p className="lunch-editor-venue">Выбрано кафе: <strong>{selected.cafe.title}</strong> <button type="button" className="scenario-option" onClick={onClear}>Снять выбор</button></p>}
    <form className="lunch-time-form" onSubmit={submit}>
      <label>Действие<select value={action} disabled={disabled || pending} onChange={(event) => setAction(event.target.value)}><option value="add">Добавить обед</option>{lunches.length > 0 && <><option value="update">Изменить или перенести обед</option><option value="remove">Убрать обед</option></>}</select></label>
      {action !== 'add' && <label>Обед<select value={lunchID} disabled={disabled || pending} onChange={(event) => setLunchID(event.target.value)}>{lunches.map((step) => <option key={step.visit_id} value={step.visit_id}>{label(step)} · {Math.round(step.lunch.duration_seconds / 60)} мин.</option>)}</select></label>}
      {action !== 'remove' && <><label className="removal-acknowledgement"><input type="checkbox" checked={freeTime} disabled={disabled || pending} onChange={(event) => setFreeTime(event.target.checked)} />Свободное время без кафе</label><label>После посещения<select value={afterID} disabled={disabled || pending} onChange={(event) => setAfterID(event.target.value)}>{visits.map((step) => <option key={step.visit_id} value={step.visit_id}>{label(step)}</option>)}</select></label><label>Длительность<select value={duration} disabled={disabled || pending} onChange={(event) => setDuration(Number(event.target.value))}><option value={2700}>45 минут</option><option value={3600}>60 минут</option></select></label></>}
      {action !== 'add' && <label className="removal-acknowledgement"><input type="checkbox" checked={acknowledged} disabled={disabled || pending} onChange={(event) => setAcknowledged(event.target.checked)} />Понимаю, что билет или регистрация не отменятся</label>}
      <button className="scenario-option scenario-primary" type="submit" disabled={disabled || pending || (action !== 'remove' && !afterID) || (action !== 'add' && !lunchID) || visits.length === 0 && action !== 'remove'}>{pending ? 'Повторить запрос кнопкой ниже' : action === 'remove' ? 'Предложить удаление' : 'Предложить изменение'}</button>
    </form>
  </section>;
}
