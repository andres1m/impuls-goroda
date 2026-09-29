import React, { useState } from 'react';
import { zonedDateTime } from './input.js';
import { localDateTime } from './scenarioForm.js';

function actualTime(value, stored, timezone) {
  if (!value) throw new Error('Укажите фактическое начало и конец посещения.');
  if (stored && value === localDateTime(stored, timezone)) return stored;
  const result = zonedDateTime(value, timezone);
  for (let minutes = -180; minutes <= 180; minutes += 15) {
    if (minutes && localDateTime(new Date(result.timestamp + minutes * 60000).toISOString(), timezone) === value) {
      throw new Error('Это время неоднозначно из-за перевода часов. Уточните время посещения.');
    }
  }
  return result.value;
}

export default function ExecutionControls({ visitID, execution, timezone, disabled, onExecution }) {
  const [editing, setEditing] = useState(false);
  const [start, setStart] = useState(() => localDateTime(execution?.actual_started_at, timezone));
  const [end, setEnd] = useState(() => localDateTime(execution?.actual_ended_at, timezone));
  const [error, setError] = useState('');
  const completed = execution?.status === 'completed';
  function save(event) {
    event.preventDefault();
    try {
      const actual_started_at = actualTime(start, execution?.actual_started_at, timezone);
      const actual_ended_at = actualTime(end, execution?.actual_ended_at, timezone);
      if (Date.parse(actual_ended_at) <= Date.parse(actual_started_at)) throw new Error('Конец посещения должен быть позже начала.');
      setError('');
      onExecution(visitID, 'completed', { actual_started_at, actual_ended_at });
    } catch (failure) { setError(failure.message); }
  }
  return <section className="server-execution-actions" aria-label="Отметка посещения">
    {completed && <p className="server-execution-status">Пройдено · по вашей отметке{execution?.actual_started_at && execution?.actual_ended_at ? '' : '. Добавьте фактическое время для пересчёта маршрута.'}</p>}
    {!editing ? <div className="server-execution-choices"><button type="button" className="scenario-option" disabled={disabled} onClick={() => setEditing(true)}>{completed ? 'Изменить фактическое время' : 'Отметить пройденным'}</button>
      {!completed && <button type="button" className="scenario-option server-execution-skip" disabled={disabled || execution?.status === 'skipped'} onClick={() => onExecution(visitID, 'skipped')}>{execution?.status === 'skipped' ? 'Отмечено пропущенным' : 'Пропустить точку'}</button>}</div> : <form className="execution-time-form" onSubmit={save}>
      <h4>Когда вы были здесь?</h4><p>Укажите фактические дату и время по местному времени ({timezone}).</p>
      <label>Начало посещения<input type="datetime-local" value={start} required disabled={disabled} onChange={(event) => setStart(event.target.value)} /></label>
      <label>Конец посещения<input type="datetime-local" value={end} required disabled={disabled} onChange={(event) => setEnd(event.target.value)} /></label>
      {error && <p role="alert">{error}</p>}
      <div className="execution-time-buttons"><button className="scenario-option scenario-primary" type="submit" disabled={disabled}>Сохранить отметку</button>
      <button className="scenario-option" type="button" disabled={disabled} onClick={() => { setEditing(false); setError(''); }}>Отмена</button></div>
    </form>}
  </section>;
}
