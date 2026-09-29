import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import { createScenario } from './scenario.js';

const themes = [
  ['vibe', 'Вайб', 'Атмосферные улицы, локальные кофейни и панорамы', 'M12 3l1.9 5.8L20 11l-6.1 2.2L12 19l-1.9-5.8L4 11l6.1-2.2L12 3z'],
  ['mood', 'Настроение', 'Неспешный маршрут для отдыха и вдохновения', 'M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16zm-3 7a1 1 0 1 1 0-2 1 1 0 0 1 0 2zm6 0a1 1 0 1 1 0-2 1 1 0 0 1 0 2zm-6.5 3.5h7a3.5 3.5 0 0 1-7 0z'],
  ['culture', 'Культура', 'Музеи, галереи, театры и исторические кварталы', 'M4 10h16v2H4v-2zm2 4h2v5H6v-5zm5 0h2v5h-2v-5zm5 0h2v5h-2v-5zM3 19h18v2H3v-2zM12 3l9 5H3l9-5z'],
  ['energy', 'Энергия', 'Динамичный темп, события и точки притяжения', 'M13 2L4 14h7l-1 8 9-12h-7l1-8z'],
  ['balance', 'Баланс', 'Прогулка по паркам, культура и уютные паузы', 'M12 3c4.5 0 8 3.5 8 8 0 5.2-8 10-8 10S4 16.2 4 11c0-4.5 3.5-8 8-8zm0 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6z'],
  ['benefit', 'Польза', 'Активное движение, набережные и новые знания', 'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm3.8 6.2-2.6 6.4-6.4 2.6 2.6-6.4 6.4-2.6z'],
];

export default function ScenarioCreate({ apiBaseUrl, accessToken, onCreated, onLibrary, onAuthRequired }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [retry, setRetry] = useState(null);
  const controller = useRef(null);
  useEffect(() => () => controller.current?.abort(), []);

  async function submit(input, previous) {
    if (busy) return;
    const attempt = previous || { input, key: crypto.randomUUID() };
    const request = new AbortController();
    controller.current = request;
    setBusy(true); setError(''); setRetry(null);
    try {
      const scenario = await createScenario(apiBaseUrl, accessToken, attempt.input, attempt.key, request.signal);
      if (!request.signal.aborted) onCreated(scenario);
    } catch (failure) {
      if (request.signal.aborted) return;
      if (failure.httpStatus === 401) onAuthRequired();
      else {
        setError('Не удалось создать сценарий. Попробуйте ещё раз.');
        setRetry(attempt);
      }
    } finally { if (!request.signal.aborted) setBusy(false); }
  }

  return <main className="entry-page scenario-create">
    <header className="scenario-topbar">
      <Brand className="entry-brand" />
      {onLibrary && <button type="button" className="route-library-nav-btn" disabled={busy} onClick={onLibrary}>Мои маршруты</button>}
    </header>
    <div className="scenario-create-hero">
      <h1>Как проведём день?</h1>
      <p>Выберите готовое настроение дня или опишите идеальный маршрут своими словами.</p>
    </div>
    <section className="scenario-create-themes" aria-label="Темы дня" aria-busy={busy}>
      {themes.map(([id, label, desc, iconPath]) => (
        <button key={id} type="button" data-theme={id} className="scenario-option scenario-theme-card" disabled={busy} onClick={() => submit({ preset_id: id })}>
          <span className="scenario-theme-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor"><path d={iconPath} /></svg>
          </span>
          <span className="scenario-theme-copy">
            <strong className="scenario-theme-title">{label}</strong>
            <span className="scenario-theme-desc">{desc}</span>
          </span>
        </button>
      ))}
    </section>
    <form className="scenario-card scenario-form scenario-custom-form" onSubmit={(event) => { event.preventDefault(); submit({ source_text: text.trim() }); }}>
      <div className="scenario-custom-head">
        <h2>Свой сценарий</h2>
        <span>Свободный запрос</span>
      </div>
      <label>Опишите пожелания к прогулке
        <textarea rows={3} maxLength={4000} value={text} disabled={busy} placeholder="Например: прогулка по центру, современное искусство, набережная и хороший кофе" onChange={(event) => setText(event.target.value)} />
      </label>
      <button className="scenario-option scenario-primary" disabled={busy || !text.trim()}>{busy ? 'Создаём…' : 'Продолжить'}</button>
    </form>
    {error && <p className="scenario-error" role="alert">{error}</p>}
    {retry && <button className="scenario-option" disabled={busy} onClick={() => submit(null, retry)}>Повторить</button>}
  </main>;
}

