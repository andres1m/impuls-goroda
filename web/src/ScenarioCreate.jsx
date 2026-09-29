import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import { createScenario } from './scenario.js';

const themes = [['vibe', 'Вайб'], ['mood', 'Настроение'], ['culture', 'Культура'], ['energy', 'Энергия'], ['balance', 'Баланс'], ['benefit', 'Польза']];

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
    <header><Brand className="entry-brand" /></header>
    <h1>Как проведём день?</h1>
    <p>Выберите тему или напишите, чего хочется.</p>
    <section className="scenario-create-themes" aria-label="Темы дня" aria-busy={busy}>
      {themes.map(([id, label]) => <button key={id} className="scenario-option" disabled={busy} onClick={() => submit({ preset_id: id })}>{label}</button>)}
    </section>
    <form className="scenario-card scenario-form" onSubmit={(event) => { event.preventDefault(); submit({ source_text: text.trim() }); }}>
      <label>Свой сценарий<textarea rows={3} maxLength={4000} value={text} disabled={busy} placeholder="Например: прогулка, современное искусство и кофе" onChange={(event) => setText(event.target.value)} /></label>
      <button className="scenario-option scenario-primary" disabled={busy || !text.trim()}>{busy ? 'Создаём…' : 'Продолжить'}</button>
    </form>
    {error && <p className="scenario-error" role="alert">{error}</p>}
    {retry && <button className="scenario-option" disabled={busy} onClick={() => submit(null, retry)}>Повторить</button>}
    <button className="scenario-option" disabled={busy} onClick={onLibrary}>Мои маршруты</button>
  </main>;
}
