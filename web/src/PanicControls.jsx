import React, { useEffect, useRef, useState } from 'react';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';
import { pinHistoryIncomplete } from './routeCommands.js';
import { coordinate } from './routeProjection.js';

export default function PanicControls({ route, mapApiKey, disabled, onPanic }) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState('already_delayed');
  const [minutes, setMinutes] = useState('30');
  const [source, setSource] = useState('device');
  const [position, setPosition] = useState(null);
  const [picking, setPicking] = useState(false);
  const [locating, setLocating] = useState(false);
  const [error, setError] = useState('');
  const active = useRef(true);
  const latest = useRef(null);
  latest.current = { revision: route.revision, disabled };
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const incomplete = pinHistoryIncomplete(route);
  const locked = disabled || locating;
  const seconds = Number(minutes) * 60;
  const validWait = /^\d+$/.test(minutes) && Number.isInteger(seconds) && seconds > 0 && seconds <= 2147483647;

  async function submit(event) {
    event.preventDefault();
    if (locked || incomplete || mode === 'future_wait' && !validWait) return;
    setError('');
    const revision = route.revision;
    let point = position;
    if (source === 'device') {
      if (!navigator.geolocation) { setError('Геолокация недоступна. Выберите точку на карте.'); return; }
      setLocating(true);
      try {
        point = await new Promise((resolve, reject) => navigator.geolocation.getCurrentPosition(({ coords }) => resolve({ latitude: coords.latitude, longitude: coords.longitude }), reject, { enableHighAccuracy: true, maximumAge: 0, timeout: 12000 }));
      } catch {
        if (active.current) { setLocating(false); setError('Не удалось определить позицию. Попробуйте снова или выберите точку на карте.'); }
        return;
      }
      if (!active.current) return;
      setLocating(false);
      if (latest.current.disabled || latest.current.revision !== revision) { setError('Маршрут изменился. Проверьте его и повторите действие.'); return; }
    }
    if (!coordinate(point)) { setError('Выберите текущую позицию.'); return; }
    const input = { delay_mode: mode, position: point, position_source: source };
    if (mode === 'already_delayed') input.effective_start_at = new Date().toISOString();
    else input.delay_seconds = seconds;
    onPanic(input);
  }

  return <section className="owner-route-actions panic-controls" aria-label="Опоздание">
    {!open ? <button className="scenario-option" disabled={disabled} onClick={() => setOpen(true)}>Опаздываю</button> : <form onSubmit={submit}>
      <h2>Когда продолжим?</h2>
      <div className="server-participation-actions">
        <button type="button" className="scenario-option" aria-pressed={mode === 'already_delayed'} disabled={locked} onClick={() => setMode('already_delayed')}>Уже опоздал</button>
        <button type="button" className="scenario-option" aria-pressed={mode === 'future_wait'} disabled={locked} onClick={() => setMode('future_wait')}>Продолжу через…</button>
      </div>
      {mode === 'future_wait' && <label className="panic-wait">Через сколько минут?<input type="number" min="1" step="1" inputMode="numeric" value={minutes} disabled={locked} onChange={(event) => setMinutes(event.target.value)} /></label>}
      <div className="server-participation-actions">
        <button type="button" className="scenario-option" aria-pressed={source === 'device'} disabled={locked} onClick={() => { setSource('device'); setPicking(false); setError(''); }}>Моя геопозиция</button>
        <button type="button" className="scenario-option" aria-pressed={source === 'manual'} disabled={locked} onClick={() => { setSource('manual'); setPicking(true); setError(''); }}>Указать на карте</button>
      </div>
      {source === 'manual' && position && <p role="status">Позиция выбрана на карте</p>}
      {picking && !locked && <TwoGisRouteMap apiKey={mapApiKey} city={route.city} stops={[]} pickMode="position" onCancelPick={() => setPicking(false)} onPick={([latitude, longitude]) => { setPosition({ latitude, longitude }); setPicking(false); }} />}
      {incomplete && <p className="scenario-error">У пройденных точек нет фактического времени. Пересчёт пока недоступен.</p>}
      {error && <p className="scenario-error" role="alert">{error}</p>}
      <div className="server-participation-actions">
        <button type="submit" className="scenario-option scenario-primary" disabled={locked || incomplete || source === 'manual' && !position || mode === 'future_wait' && !validWait}>{locating ? 'Определяем позицию…' : 'Предложить изменения'}</button>
        <button type="button" className="scenario-option" disabled={locked} onClick={() => setOpen(false)}>Закрыть</button>
      </div>
    </form>}
  </section>;
}
