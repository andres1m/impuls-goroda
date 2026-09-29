import React, { useEffect, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import ExtractionReview from './ExtractionReview.jsx';
import ScenarioVariants from './ScenarioVariants.jsx';
import useClosingConfirmation from './useClosingConfirmation.js';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import { loadOwnerRoute, loadSelectedRoute, RouteRequestError } from './route.js';
import { createRouteAttempt, sendRouteCommand, terminalRouteError } from './routeCommands.js';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';
import { completeScenario, createCompletionAttempt, createDraftAttempt, loadScenario, saveScenarioDraft } from './scenario.js';
import { confirmedScenarioInput, draftScenarioInput, localDateTime, resolveStartCity, scenarioForm } from './scenarioForm.js';
import { cities } from './input.js';
import { userMessage } from './messages.js';
import { categories, interests, movementModes } from './taxonomy.js';

const presetNames = { vibe: 'Вайб', mood: 'Настроение', culture: 'История и культура', energy: 'Энергия', balance: 'Баланс', benefit: 'Движение и польза' };
const modeNames = { live: 'Актуальные данные', prepared: 'Подготовленные данные', synthetic: 'Демонстрационные данные' };
const profiles = [['relaxed', 'Спокойный'], ['moderate', 'Умеренный'], ['intense', 'Активный']];
const budgetModes = [['none', 'Без ограничения'], ['advisory', 'Ориентир'], ['strict', 'Не превышать']];
const popularInterests = new Set(['city_walk', 'classical_art', 'contemporary_art', 'gastro_coffee', 'excursions', 'performing_arts']);
const interestPresets = [
  ['culture', 'Культура и искусство', ['classical_art', 'contemporary_art', 'performing_arts', 'excursions']],
  ['walk_coffee', 'Прогулки и кофе', ['city_walk', 'gastro_coffee', 'excursions']],
  ['active', 'Актив и впечатления', ['running_park', 'street_workout', 'science_tech', 'lectures_workshops']],
];
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
  </dl>{c.obligations?.map((item, index) => <p key={index}>Обязательное посещение {index + 1}: {item.session_id || item.visit_id}, {instant(item.starts_at, input.timezone)}; запас {Math.ceil(item.arrival_buffer_seconds / 60)} мин.; участие: {item.participation}.</p>)}</details>;
}

export default function ScenarioEntry({ scenario, apiBaseUrl, accessToken, mapApiKey, onLibrary }) {
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
  const [saving, setSaving] = useState(false);
  const [savePending, setSavePending] = useState(false);
  const [extractionChoice, setExtractionChoice] = useState(null);
  const [showAllInterests, setShowAllInterests] = useState(false);
  const draftRequest = useRef(null);
  const savingRef = useRef(false);
  const signature = JSON.stringify({ form, city, origin, destination });
  const [savedSignature, setSavedSignature] = useState(signature);
  const dirty = signature !== savedSignature;
  const request = useRef(null);
  const geoRequest = useRef(null);
  const selection = useRef(null);
  useEffect(() => () => { request.current?.abort(); geoRequest.current?.abort(); }, []);
  const locked = busy || attempt !== null || saving || savePending;
  useClosingConfirmation(Boolean(current.status === 'draft' && dirty || savePending || attempt));

  async function saveConditions() {
    if (savingRef.current || busy || attempt || locating || current.status !== 'draft') return;
    let command = draftRequest.current;
    if (!command) {
      try {
        command = { attempt: createDraftAttempt(current, draftScenarioInput(current.input, form, city, origin, destination)), signature, acknowledged: false, conflicted: false };
      } catch (failure) { setError(failure.message); return; }
      draftRequest.current = command;
    }
    savingRef.current = true; setSaving(true); setSavePending(true); setReview(null); setError('');
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    try {
      if (!command.acknowledged && !command.conflicted) {
        const saved = await saveScenarioDraft(apiBaseUrl, accessToken, command.attempt, fetch, controller.signal);
        command.acknowledged = true; command.savedVersion = saved.version;
      }
      const latest = await loadScenario(apiBaseUrl, accessToken, current.scenario_id, fetch, controller.signal);
      if (controller.signal.aborted) return;
      setCurrent(latest);
      if (command.acknowledged && latest.version === command.savedVersion) {
        setSavedSignature(command.signature);
        setError('Условия сохранены. Можно продолжить позже.');
      } else setError('Сценарий изменился в другой сессии. Ваш ввод оставлен на экране — проверьте его перед новым сохранением.');
      draftRequest.current = null; setSavePending(false);
    } catch (failure) {
      if (controller.signal.aborted) return;
      if (['SCENARIO_VERSION_CONFLICT', 'SCENARIO_ALREADY_COMPLETED'].includes(failure.code)) {
        command.conflicted = true;
        setError('Сценарий изменился. Нажмите «Обновить сценарий»; введённые поля останутся на экране.');
      } else if (failure.httpStatus >= 400 && failure.httpStatus < 500 && ![401, 408, 429].includes(failure.httpStatus)) {
        draftRequest.current = null; setSavePending(false);
        setError('Не удалось сохранить условия. Проверьте поля перед новым сохранением.');
      } else setError(failure.httpStatus === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.' : 'Ответ не получен. Повторите сохранение — команда и введённые условия сохранены в этом окне.');
    } finally {
      savingRef.current = false;
      if (!controller.signal.aborted) setSaving(false);
    }
  }
  function change(field, value) { setForm((old) => ({ ...old, [field]: value })); setReview(null); setError(''); }
  function changeDay(day) {
    setForm((old) => ({ ...old, day,
      start: day && old.start?.includes('T') ? `${day}T${old.start.slice(11)}` : '',
      end: day && old.end?.includes('T') ? `${day}T${old.end.slice(11)}` : '',
    }));
    setReview(null); setError('');
  }
  function toggleLunch(enabled) {
    setForm((old) => {
      return { ...old, lunchEnabled: enabled,
        lunchStart: old.lunchStart || (enabled ? '13:00' : ''),
        lunchEnd: old.lunchEnd || (enabled ? '14:30' : ''),
      };
    });
    setReview(null); setError('');
  }
  function toggle(field, value) { change(field, form[field].includes(value) ? form[field].filter((item) => item !== value) : [...form[field], value]); }
  function applyExtraction(proposal) {
    if (locked) return;
    setForm((old) => ({ ...old, ...proposal.formPatch }));
    setExtractionChoice('used'); setReview(null); setError('');
  }
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
  async function calculate(confirmed) {
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    const command = attempt || createCompletionAttempt(current, confirmed || review);
    setAttempt(command); setBusy(true); setError('');
    try {
      await completeScenario(apiBaseUrl, accessToken, command, fetch, controller.signal);
      await refresh(controller);
      if (!controller.signal.aborted) setSavedSignature(signature);
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
    if (current.pending_extraction && !extractionChoice) { setError('Перенесите предложенные условия или выберите «Заполнить самостоятельно».'); return; }
    try { const input = confirmedScenarioInput(current.input, form, city, origin, destination); setReview(input); setError(''); calculate(input); }
    catch (failure) { setError(failure.message); }
  }
  if (route) return <OwnerRouteScreen key={route.route_id} route={route} variantIDs={current.outcome?.route_ids || []} apiBaseUrl={apiBaseUrl} accessToken={accessToken} mapApiKey={mapApiKey} onBack={() => setRoute(null)} />;
  const outcome = current.outcome;
  const earliestStart = cities[city] ? localDateTime(new Date(Math.ceil((Date.now() + 1) / 60000) * 60000).toISOString(), cities[city].timezone) : '';
  const today = earliestStart.slice(0, 10);
  const visibleInterests = showAllInterests
    ? interests
    : interests.filter(([code]) => popularInterests.has(code) || form.interests.includes(code));
  const hiddenInterestsCount = interests.length - visibleInterests.length;
  return <main className="scenario-page">
    <header className="scenario-topbar">
      <Brand className="entry-brand" />
      {onLibrary && <button type="button" className="route-library-nav-btn" onClick={onLibrary} disabled={busy || saving}>Мои маршруты</button>}
    </header>
    <div className="scenario-hero">
      <span className="scenario-hero-badge">{current.source === 'preset' ? 'Тематический день' : 'Персональный маршрут'}</span>
      <h1>{current.source === 'preset' ? presetNames[current.preset_id] : 'Ваш сценарий'}</h1>
      {current.source_text && <p className="scenario-description">{current.source_text}</p>}
    </div>
    {current.status === 'draft' && current.pending_extraction && <ExtractionReview input={current.pending_extraction} choice={extractionChoice} disabled={locked} onApply={applyExtraction} onManual={() => { setExtractionChoice('manual'); setReview(null); }} />}
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
        {current.source === 'preset' && <p className="scenario-timezone">Интересы и темп можно изменить под себя.</p>}
        <fieldset disabled={locked}>
          <legend className="scenario-timezone">{cities[city] ? `Время местное · ${cities[city].name}` : 'Сначала выберите старт, чтобы определить часовой пояс'}</legend>
          <label>День прогулки<input type="date" value={form.day} min={today} onChange={(e) => changeDay(e.target.value)} required /></label>
          <div className="scenario-endpoints">
            <label>Начало<input type="time" value={form.start.slice(11)} min={form.day === today ? earliestStart.slice(11) : undefined} disabled={!form.day} onChange={(e) => change('start', `${form.day}T${e.target.value}`)} required /></label>
            <label>Конец<input type="time" value={form.end.slice(11)} min={form.start.slice(11)} disabled={!form.day} onChange={(e) => change('end', `${form.day}T${e.target.value}`)} required /></label>
          </div>
          <button type="button" className="scenario-option scenario-quick-conditions" onClick={() => { setForm((old) => ({ ...old, modes: ['walk'], profile: 'moderate', budgetMode: 'none', budget: '' })); setReview(null); setError(''); }}>Быстрый вариант: пешком · умеренно · без лимита</button>
          <fieldset><legend>Передвижение</legend><div className="scenario-choices scenario-chips">{[...movementModes, ...form.modes.filter((code) => !movementModes.some(([value]) => value === code)).map((code) => [code, code])].map(([code, label]) => <label key={code}><input type="checkbox" checked={form.modes.includes(code)} onChange={() => toggle('modes', code)} /><span>{label}</span></label>)}</div></fieldset>
          <div className="scenario-endpoints">
            <label>Темп<select value={form.profile} onChange={(e) => change('profile', e.target.value)} required><option value="">Выберите темп</option>{profiles.map(([code, label]) => <option key={code} value={code}>{label}</option>)}{form.profile && !profiles.some(([code]) => code === form.profile) && <option value={form.profile}>{form.profile}</option>}</select></label>
            <label>Бюджет<select value={form.budgetMode} onChange={(e) => change('budgetMode', e.target.value)} required><option value="">Выберите условие</option>{budgetModes.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</select></label>
          </div>
          {form.budgetMode && form.budgetMode !== 'none' && <label>Сумма, ₽<input inputMode="decimal" value={form.budget} onChange={(e) => change('budget', e.target.value)} required /></label>}
          <fieldset className="scenario-interests-fieldset">
            <legend>Интересы{form.interests.length ? ` · выбрано ${form.interests.length}` : ''}</legend>
            <div className="scenario-presets-bar" role="group" aria-label="Быстрые наборы интересов">
              {interestPresets.map(([id, title, codes]) => {
                const active = codes.every((c) => form.interests.includes(c)) && form.interests.length === codes.length;
                return <button key={id} type="button" className={`scenario-preset-pill${active ? ' is-active' : ''}`} onClick={() => change('interests', codes)}>{title}</button>;
              })}
              {form.interests.length > 0 && <button type="button" className="scenario-preset-pill scenario-preset-reset" onClick={() => change('interests', [])}>Сбросить</button>}
            </div>
            <div className="scenario-choices scenario-chips">
              {visibleInterests.map(([code, label]) => <label key={code}><input type="checkbox" checked={form.interests.includes(code)} onChange={() => toggle('interests', code)} /><span>{label}</span></label>)}
              {(hiddenInterestsCount > 0 || showAllInterests) && (
                <button type="button" className="scenario-chip-more" onClick={() => setShowAllInterests((prev) => !prev)}>
                  {showAllInterests ? 'Свернуть' : `+ Ещё ${hiddenInterestsCount} тем`}
                </button>
              )}
            </div>
          </fieldset>
          <fieldset><legend>Обед</legend>
            <label className="scenario-check"><input type="checkbox" checked={form.lunchEnabled} onChange={(e) => toggleLunch(e.target.checked)} />Запланировать обед</label>
            {form.lunchEnabled && <>
              <div className="scenario-endpoints">
                <label>Начать с<input type="time" value={form.lunchStart} onChange={(e) => change('lunchStart', e.target.value)} required /></label>
                <label>Закончить до<input type="time" value={form.lunchEnd} onChange={(e) => change('lunchEnd', e.target.value)} required /></label>
              </div>
              <label>Длительность<select value={form.lunchDuration} onChange={(e) => change('lunchDuration', e.target.value)}>
                <option value="2700">45 минут</option><option value="3600">60 минут</option>
                {!['2700', '3600'].includes(form.lunchDuration) && <option value={form.lunchDuration}>Из сохранённых условий</option>}
              </select></label>
              <p className="scenario-timezone">Если подходящего кафе нет, возможна свободная пауза. Если обед не поместится, расчёт покажет предупреждение.</p>
            </>}
          </fieldset>
          <details className="scenario-advanced"><summary>Дополнительные условия{form.excluded.length ? ` · исключено ${form.excluded.length}` : ''}</summary>
          <fieldset><legend>Не включать</legend><div className="scenario-choices scenario-chips scenario-chips-exclude">{categories.map(([code, label]) => <label key={code}><input type="checkbox" checked={form.excluded.includes(code)} onChange={() => toggle('excluded', code)} /><span>{label}</span></label>)}</div></fieldset>
          <label className="scenario-check"><input type="checkbox" checked={form.pushkin} onChange={(e) => change('pushkin', e.target.checked)} />Только события по Пушкинской карте</label>
          <label>Пожелания<textarea value={form.wishes} onChange={(e) => change('wishes', e.target.value)} rows={3} placeholder="Например: больше видовых точек или спокойные улочки…" /></label>
          </details>
        </fieldset>
        <RetainedConditions input={{ ...current.input, constraints: { ...current.input.constraints, excluded_categories: form.excluded } }} />
        <button type="button" className="scenario-option" disabled={saving || busy || Boolean(attempt) || locating || !dirty && !savePending} onClick={saveConditions}>{saving ? 'Сохраняем…' : draftRequest.current?.conflicted ? 'Обновить сценарий' : savePending ? 'Повторить сохранение' : 'Сохранить условия'}</button>
        {dirty && <p className="scenario-timezone" role="status">Есть несохранённые изменения. Сохраните условия, чтобы продолжить после закрытия.</p>}
        {!review && !attempt && <button className="scenario-option scenario-primary" disabled={locked || locating || !city || !origin}>Построить маршрут</button>}
      </section>
    </form>}
    {review && <section className="scenario-card" aria-label="Расчёт маршрута"><h2>{busy ? 'Строим маршрут…' : 'Условия маршрута'}</h2>
      <p>{instant(review.start_at, review.timezone)} — {instant(review.end_at, review.timezone)}</p>
      <p>{review.constraints.movement_modes.map((code) => nameOf(movementModes, code)).join(', ')} · {nameOf(profiles, review.constraints.load_profile)}</p>
      <p>{form.interests.map((code) => nameOf(interests, code)).join(', ') || 'Без дополнительных предпочтений по интересам'}</p>
      <p>Бюджет: {nameOf(budgetModes, review.constraints.budget.mode)}{review.constraints.budget.limit ? ` · ${form.budget} ₽` : ''}</p>
      {review.constraints.lunch_window ? <p>Обед: {instant(review.constraints.lunch_window.start_at, review.timezone)} — {instant(review.constraints.lunch_window.end_at, review.timezone)}. Запрошенная длительность: {Math.ceil(review.constraints.lunch_window.min_duration_seconds / 60)} мин.</p> : <p>Без запланированного обеда</p>}
      <p>Старт выбран на карте · {destination ? 'финиш выбран' : 'без заданного финиша'}</p>
      {review.constraints.pushkin_card_only && <p>Только события по Пушкинской карте</p>}
      {review.constraints.excluded_categories.length > 0 && <p>Исключены: {review.constraints.excluded_categories.map((code) => nameOf(categories, code)).join(', ')}</p>}
      {review.constraints.semantic_query && <p className="scenario-description">{review.constraints.semantic_query}</p>}
      {!attempt && <button className="scenario-option" onClick={() => setReview(null)}>Изменить условия</button>}
      <button className="scenario-option scenario-primary" disabled={busy} onClick={() => calculate()}>{busy ? 'Рассчитываем…' : attempt ? 'Повторить расчёт' : 'Построить маршрут'}</button>
    </section>}
    {outcome && <section className="scenario-card" aria-live="polite"><h2>{current.status === 'completed' ? 'Варианты маршрута' : 'Условия нужно уточнить'}</h2>
      <p>{modeNames[outcome.data_mode]}</p>
      {outcome.conflicts.map((item, index) => <p key={`${item.code}-${index}`}>{userMessage(item)}</p>)}
      {outcome.warnings.length > 0 && <details className="route-notes"><summary>Условия посещений</summary>{outcome.warnings.map((item, index) => <p key={`${item.code}-${index}`}>{userMessage(item)}</p>)}</details>}
      {outcome.route_ids.length > 0 && <ScenarioVariants key={`${current.scenario_id}:${current.version}`} routeIDs={outcome.route_ids} apiBaseUrl={apiBaseUrl} accessToken={accessToken} disabled={locked} loadingID={loadingID} retrySelectionID={retrySelectionID} onSelect={openRoute} />}
    </section>}
    {error && <p className="scenario-error" role="alert">{error}</p>}
  </main>;
}
