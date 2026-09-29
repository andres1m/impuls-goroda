import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import { loadOwnerRoute, loadSelectedRoute, RouteRequestError } from './route.js';
import { createRouteAttempt, sendRouteCommand, terminalRouteError } from './routeCommands.js';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';
import { completeScenario, createCompletionAttempt, loadScenario } from './scenario.js';
import { confirmedScenarioInput, resolveStartCity, scenarioForm } from './scenarioForm.js';
import { cities } from './input.js';
import { categories, interests, movementModes } from './taxonomy.js';

const presetNames = { vibe: 'Вайб', mood: 'Настроение', culture: 'История и культура', energy: 'Энергия', balance: 'Баланс', benefit: 'Движение и польза' };
const modeNames = { live: 'Актуальные данные', prepared: 'Подготовленные данные', synthetic: 'Демонстрационные данные' };
const profiles = [['relaxed', 'Спокойный'], ['moderate', 'Умеренный'], ['intense', 'Активный']];
const budgetModes = [['none', 'Без ограничения'], ['advisory', 'Ориентир'], ['strict', 'Не превышать']];
const nameOf = (options, code) => options.find(([value]) => value === code)?.[1] || code;
function instant(value, timezone) {
  if (!value) return 'Не задано';
  try { return new Intl.DateTimeFormat('ru-RU', { timeZone: timezone, dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)); }
  catch { return value; }
}
function RetainedConditions({ input }) {
  const c = input.constraints || {};
  return <details className="scenario-retained"><summary>Другие условия из бота</summary><p>Эти условия сохраняются при расчёте.</p><dl>
    <div><dt>Исключены</dt><dd>{c.excluded_categories?.map((code) => nameOf(categories, code)).join(', ') || 'Нет'}</dd></div>
    <div><dt>Льготные программы</dt><dd>{c.benefit_programs?.join(', ') || 'Нет'}</dd></div>
    <div><dt>Категории участника</dt><dd>{c.audience_claims?.map((item) => item.audience).join(', ') || 'Не указаны'}</dd></div>
    <div><dt>Предпочтения</dt><dd>{c.soft_preferences?.join(', ') || 'Нет'}</dd></div>
    <div><dt>Допустимые неопределённости</dt><dd>{c.accepted_unknowns?.join(', ') || 'Не разрешены'}</dd></div>
    {c.lunch_window && <div><dt>Обед</dt><dd>{instant(c.lunch_window.start_at, input.timezone)} — {instant(c.lunch_window.end_at, input.timezone)}, минимум {Math.ceil(c.lunch_window.min_duration_seconds / 60)} мин.</dd></div>}
  </dl>{c.obligations?.map((item, index) => <p key={index}>Обязательное посещение {index + 1}: {item.session_id || item.visit_id}, {instant(item.starts_at, input.timezone)}; запас {Math.ceil(item.arrival_buffer_seconds / 60)} мин.; участие: {item.participation}.</p>)}</details>;
}

export default function ScenarioEntry({ scenario, apiBaseUrl, accessToken, mapApiKey }) {
  const [current, setCurrent] = useState(scenario);
  const [form, setForm] = useState(() => scenarioForm(scenario.input));
  const [city, setCity] = useState(scenario.input.city || '');
  const [origin, setOrigin] = useState(scenario.input.origin || null);
  const [destination, setDestination] = useState(scenario.input.destination || null);
  const [pickMode, setPickMode] = useState(null);
  const [locating, setLocating] = useState(false);
  const [review, setReview] = useState(null);
  const [attempt, setAttempt] = useState(null);
  const [busy, setBusy] = useState(false);
  const [route, setRoute] = useState(null);
  const [loadingID, setLoadingID] = useState(null);
  const [retrySelectionID, setRetrySelectionID] = useState(null);
  const [error, setError] = useState('');
  const request = useRef(null);
  const geoRequest = useRef(null);
  const selection = useRef(null);
  useEffect(() => () => { request.current?.abort(); geoRequest.current?.abort(); }, []);
  const locked = busy || attempt !== null;
  function change(field, value) { setForm((old) => ({ ...old, [field]: value })); setReview(null); setError(''); }
  function toggle(field, value) { change(field, form[field].includes(value) ? form[field].filter((item) => item !== value) : [...form[field], value]); }
  async function pickPoint([latitude, longitude]) {
    const point = { latitude, longitude };
    const target = pickMode;
    setPickMode(null); setReview(null);
    if (target === 'finish') { setDestination(point); return; }
    geoRequest.current?.abort();
    const controller = new AbortController(); geoRequest.current = controller;
    setOrigin(point); setCity(''); setLocating(true); setError('');
    try {
      const value = await resolveStartCity(point, mapApiKey, controller.signal);
      if (!controller.signal.aborted) setCity(value);
    } catch (failure) { if (!controller.signal.aborted) setError(failure.message || 'Не удалось определить город старта.'); }
    finally { if (!controller.signal.aborted) setLocating(false); }
  }
  async function refresh(controller) {
    const value = await loadScenario(apiBaseUrl, accessToken, current.scenario_id, fetch, controller.signal);
    if (!controller.signal.aborted) { setCurrent(value); setAttempt(null); setReview(null); }
  }
  async function calculate() {
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    const command = attempt || createCompletionAttempt(current, review);
    setAttempt(command); setBusy(true); setError('');
    try {
      await completeScenario(apiBaseUrl, accessToken, command, fetch, controller.signal);
      await refresh(controller);
    } catch (failure) {
      if (controller.signal.aborted) return;
      if (['SCENARIO_VERSION_CONFLICT', 'SCENARIO_ALREADY_COMPLETED'].includes(failure.code)) {
        try { await refresh(controller); if (!controller.signal.aborted) setError('Сценарий обновлён. Проверьте условия перед новым расчётом.'); }
        catch { if (!controller.signal.aborted) setError('Не удалось обновить сценарий. Повторите запрос.'); }
      } else if (failure.httpStatus >= 400 && failure.httpStatus < 500 && ![401, 408, 429].includes(failure.httpStatus)) {
        setAttempt(null); setReview(null);
        setError('Gateway отклонил запрос. Проверьте условия перед повторным расчётом.');
      } else setError(failure.httpStatus === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.' : 'Не удалось получить результат расчёта. Повторите запрос — ваши условия сохранены.');
    } finally { if (!controller.signal.aborted) setBusy(false); }
  }
  async function openRoute(id) {
    if (loadingID !== null || retrySelectionID && retrySelectionID !== id) return;
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    setLoadingID(id); setError('');
    try {
      if (!selection.current) {
        const value = await loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal);
        if (controller.signal.aborted) return;
        selection.current = { attempt: createRouteAttempt('selection', value), acknowledged: false };
      }
      const command = selection.current;
      if (!command.acknowledged) {
        const result = await sendRouteCommand(apiBaseUrl, accessToken, command.attempt, fetch, controller.signal);
        if (controller.signal.aborted) return;
        if (result.status === 'CONFLICT') {
          selection.current = null; setRetrySelectionID(null);
          setError(result.conflicts.map((item) => item.message).join(' ') || 'Не удалось выбрать вариант.');
          return;
        }
        command.acknowledged = true;
      }
      const value = await loadSelectedRoute(apiBaseUrl, accessToken, fetch, controller.signal);
      if (controller.signal.aborted) return;
      if (!value || value.route_id.toLowerCase() !== command.attempt.routeID) {
        selection.current = null; setRetrySelectionID(null);
        setError('Выбор изменился в другой сессии. Нажмите на нужный вариант снова.');
        return;
      }
      selection.current = null; setRetrySelectionID(null); setRoute(value);
    }
    catch (failure) {
      if (controller.signal.aborted) return;
      if (terminalRouteError(failure)) {
        selection.current = null; setRetrySelectionID(null);
        setError(failure.code === 'REVISION_CONFLICT' ? 'Вариант изменился. Нажмите на него снова, чтобы открыть актуальную версию.' : 'Вариант недоступен. Попробуйте выбрать другой.');
      } else {
        setRetrySelectionID(selection.current ? id : null);
        setError(failure instanceof RouteRequestError && failure.status === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.' : 'Не удалось открыть вариант. Повторите запрос.');
      }
    }
    finally { if (!controller.signal.aborted) setLoadingID(null); }
  }
  function prepare(event) {
    event.preventDefault();
    if (locked || locating) return;
    try { setReview(confirmedScenarioInput(current.input, form, city, origin, destination)); setError(''); }
    catch (failure) { setError(failure.message); }
  }
  if (route) return <OwnerRouteScreen key={route.route_id} route={route} apiBaseUrl={apiBaseUrl} accessToken={accessToken} mapApiKey={mapApiKey} onBack={() => setRoute(null)} />;
  const outcome = current.outcome;
  return <main className="scenario-page">
    <header><Brand /><h1>{current.source === 'preset' ? presetNames[current.preset_id] : 'Ваш сценарий'}</h1></header>
    {current.source_text && <p className="scenario-description">{current.source_text}</p>}
    {current.status === 'draft' && <form onSubmit={prepare}>
      <section className="scenario-card"><h2>Начало и конец маршрута</h2>
        <div className="scenario-endpoints">
          <button type="button" className="scenario-option" disabled={locked || locating} onClick={() => setPickMode('start')}>{origin ? 'Изменить старт' : 'Выбрать старт'}</button>
          <button type="button" className="scenario-option" disabled={locked || locating} onClick={() => setPickMode('finish')}>{destination ? 'Изменить финиш' : 'Выбрать финиш'}</button>
        </div>
        <p aria-live="polite">{locating ? 'Определяем город старта…' : origin ? `Старт выбран${cities[city] ? ` · ${cities[city].name}` : ''}` : 'Выберите точку на карте'}</p>
        {destination && <button type="button" className="scenario-option" disabled={locked} onClick={() => { setDestination(null); setReview(null); }}>Без заданного финиша</button>}
        {pickMode && <TwoGisRouteMap apiKey={mapApiKey} city={city || 'perm'} stops={[]} pickMode={pickMode} onCancelPick={() => setPickMode(null)} onPick={pickPoint} />}
      </section>
      <section className="scenario-card scenario-form"><h2>Условия прогулки</h2>
        <fieldset disabled={locked}>
          <legend className="scenario-timezone">{cities[city] ? `Время местное · ${cities[city].name}` : 'Сначала выберите старт, чтобы определить часовой пояс'}</legend>
          <div className="scenario-endpoints">
            <label>Начало<input type="datetime-local" value={form.start} onChange={(e) => change('start', e.target.value)} required /></label>
            <label>Завершение<input type="datetime-local" value={form.end} onChange={(e) => change('end', e.target.value)} required /></label>
          </div>
          <fieldset><legend>Передвижение</legend><div className="scenario-choices">{[...movementModes, ...form.modes.filter((code) => !movementModes.some(([value]) => value === code)).map((code) => [code, code])].map(([code, label]) => <label key={code}><input type="checkbox" checked={form.modes.includes(code)} onChange={() => toggle('modes', code)} />{label}</label>)}</div></fieldset>
          <label>Темп<select value={form.profile} onChange={(e) => change('profile', e.target.value)} required><option value="">Выберите темп</option>{profiles.map(([code, label]) => <option key={code} value={code}>{label}</option>)}{form.profile && !profiles.some(([code]) => code === form.profile) && <option value={form.profile}>{form.profile}</option>}</select></label>
          <fieldset><legend>Интересы</legend><div className="scenario-choices">{interests.map(([code, label]) => <label key={code}><input type="checkbox" checked={form.interests.includes(code)} onChange={() => toggle('interests', code)} />{label}</label>)}</div></fieldset>
          <label>Бюджет<select value={form.budgetMode} onChange={(e) => change('budgetMode', e.target.value)} required><option value="">Выберите условие</option>{budgetModes.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</select></label>
          {form.budgetMode && form.budgetMode !== 'none' && <label>Сумма, ₽<input inputMode="decimal" value={form.budget} onChange={(e) => change('budget', e.target.value)} required /></label>}
          <label className="scenario-check"><input type="checkbox" checked={form.pushkin} onChange={(e) => change('pushkin', e.target.checked)} />Только события по Пушкинской карте</label>
          <label>Пожелания<textarea value={form.wishes} onChange={(e) => change('wishes', e.target.value)} rows={3} /></label>
        </fieldset>
        <RetainedConditions input={current.input} />
        {!review && !attempt && <button className="scenario-option" disabled={locating || !city || !origin}>Проверить условия</button>}
      </section>
    </form>}
    {review && <section className="scenario-card" aria-label="Подтверждение расчёта"><h2>Всё верно?</h2>
      <p>{instant(review.start_at, review.timezone)} — {instant(review.end_at, review.timezone)}</p>
      <p>{review.constraints.movement_modes.map((code) => nameOf(movementModes, code)).join(', ')} · {nameOf(profiles, review.constraints.load_profile)}</p>
      <p>{form.interests.map((code) => nameOf(interests, code)).join(', ') || 'Без дополнительных предпочтений по интересам'}</p>
      <p>Бюджет: {nameOf(budgetModes, review.constraints.budget.mode)}{review.constraints.budget.limit ? ` · ${form.budget} ₽` : ''}</p>
      <p>Старт выбран на карте · {destination ? 'финиш выбран' : 'без заданного финиша'}</p>
      {review.constraints.pushkin_card_only && <p>Только события по Пушкинской карте</p>}
      {review.constraints.semantic_query && <p className="scenario-description">{review.constraints.semantic_query}</p>}
      {!attempt && <button className="scenario-option" onClick={() => setReview(null)}>Изменить условия</button>}
      <button className="scenario-option scenario-primary" disabled={busy} onClick={calculate}>{busy ? 'Рассчитываем…' : attempt ? 'Повторить запрос' : 'Подтвердить и рассчитать'}</button>
    </section>}
    {outcome && <section className="scenario-card" aria-live="polite"><h2>{current.status === 'completed' ? 'Варианты маршрута' : 'Условия нужно уточнить'}</h2>
      <p>{modeNames[outcome.data_mode]}</p>
      {[...outcome.conflicts, ...outcome.warnings].map((item, index) => <p key={`${item.code}-${index}`}>{item.message}</p>)}
      {outcome.route_ids.map((id, index) => <button className="scenario-option" key={id} disabled={loadingID !== null || locked || retrySelectionID !== null && retrySelectionID !== id} onClick={() => openRoute(id)}>{loadingID === id ? 'Открываем…' : retrySelectionID === id ? 'Повторить выбор варианта' : `Выбрать вариант ${index + 1}`}</button>)}
    </section>}
    {error && <p className="scenario-error" role="alert">{error}</p>}
    {current.pending_extraction && <p className="scenario-description">Предложенные ботом условия ещё требуют отдельного подтверждения и не включены в расчёт.</p>}
  </main>;
}
