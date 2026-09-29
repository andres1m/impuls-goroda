import React, { useEffect, useState } from 'react';
import { loadOwnerRoute } from './route.js';
import { summarizeVariant, variantDifference } from './variantSummary.js';
import { travelTitles } from './routeProjection.js';
import { userMessage } from './messages.js';

const dataNames = { live: 'Актуальные данные', prepared: 'Подготовленные данные', synthetic: 'Демонстрационные данные' };

function VariantCard({ id, index, apiBaseUrl, accessToken, disabled, selecting, retrySelection, onSelect, onSummary, difference }) {
  const [state, setState] = useState({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let ignore = false;
    setState({ kind: 'loading', apiBaseUrl, accessToken }); onSummary(id, null);
    loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal).then((route) => {
      const summary = summarizeVariant(route);
      if (!ignore) { setState({ kind: 'ready', summary, apiBaseUrl, accessToken }); onSummary(id, summary); }
    }).catch((failure) => {
      if (!ignore) setState({ kind: failure.status === 404 ? 'unavailable' : failure.status === 401 ? 'expired' : 'error', apiBaseUrl, accessToken });
    });
    return () => { ignore = true; controller.abort(); };
  }, [apiBaseUrl, accessToken, id, attempt, onSummary]);
  const visible = state.apiBaseUrl === apiBaseUrl && state.accessToken === accessToken ? state : { kind: 'loading' };
  if (visible.kind !== 'ready') return <article className="variant-card" aria-label={`Вариант ${index + 1}`}>
    <h3>Вариант {index + 1}</h3>
    <p role="status">{visible.kind === 'loading' ? 'Загружаем сохранённый маршрут…' : visible.kind === 'unavailable' ? 'Этот вариант больше недоступен.' : visible.kind === 'expired' ? 'Сессия закончилась. Откройте Mini App заново в MAX.' : 'Не удалось загрузить этот вариант.'}</p>
    {visible.kind === 'error' && <button type="button" className="scenario-option" disabled={disabled} onClick={() => setAttempt((old) => old + 1)}>Повторить загрузку</button>}
    {retrySelection && <button type="button" className="scenario-option" disabled={disabled} onClick={() => onSelect(id)}>Повторить выбор</button>}
  </article>;
  return <VariantDetails id={id} summary={state.summary} difference={difference} disabled={disabled} selecting={selecting} retrySelection={retrySelection} onSelect={onSelect} />;
}

function VariantDetails({ id, summary: s, difference, disabled, selecting, retrySelection, onSelect }) {
  return <article className="variant-card">
    <h3>{s.title}</h3><p className="variant-window">{s.window} · местное время</p>
    <div className="variant-labels">{s.dataModes.map((mode) => <span key={mode}>{dataNames[mode]}</span>)}</div>
    {difference && <p className="variant-difference">{difference}</p>}
    <dl>
      <div><dt>Посещения</dt><dd>{s.visits.length}{s.pauses ? ` · пауз: ${s.pauses}` : ''}</dd></div>
      <div><dt>Переходы по расписанию</dt><dd>{s.travelMinutes} мин. · пешком {s.walkMinutes} мин.</dd></div>
      <div><dt>Известные личные расходы</dt><dd>{s.personalCost}<small>Включая транспорт: {s.transportCost}</small></dd></div>
    </dl>
    {s.modes.length > 0 && <p className="variant-note">{s.modes.map((mode) => travelTitles[mode]).join(', ')}</p>}
    {s.unknownCostCount > 0 && <p className="variant-warning">Часть расходов неизвестна — итоговая стоимость может отличаться.</p>}
    {s.uncertainLegs > 0 && <p className="variant-warning">Есть приблизительные или непроверенные переходы.</p>}
    {s.issueCount > 0 && <p className="variant-warning">В маршруте есть изменения или проблемы. Проверьте их перед выходом.</p>}
    <ol className="variant-visits">{s.visits.map((visit, index) => <li key={index}><span>{index + 1}</span>{visit.title}</li>)}</ol>
    {s.warnings.length > 0 && <details className="variant-notices"><summary>Условия посещений</summary>{s.warnings.map((message, index) => <p key={index}>{userMessage(message)}</p>)}</details>}
    <button type="button" className="scenario-option scenario-primary" disabled={disabled} onClick={() => onSelect(id)}>{selecting ? 'Открываем…' : retrySelection ? 'Повторить выбор' : 'Выбрать этот вариант'}</button>
  </article>;
}

export default function ScenarioVariants({ routeIDs, apiBaseUrl, accessToken, disabled, loadingID, retrySelectionID, onSelect }) {
  const [summaries, setSummaries] = useState({});
  const [report] = useState(() => (id, value) => setSummaries((old) => ({ ...old, [id]: value })));
  const ready = routeIDs.filter((id) => summaries[id]);
  return <>
    {routeIDs.length > 1 && <p className="variant-note">Сравните посещения, переходы и расходы. Карточки загружаются независимо.</p>}
    <div className="scenario-variants">{routeIDs.map((id, index) => <div className="variant-column" key={id}>
      <VariantCard id={id} index={index} apiBaseUrl={apiBaseUrl} accessToken={accessToken} difference={summaries[id] && ready.length > 1 ? variantDifference(summaries[id], ready.filter((other) => other !== id).map((other) => summaries[other])) : null} disabled={disabled || loadingID !== null || retrySelectionID !== null && retrySelectionID !== id} selecting={loadingID === id} retrySelection={retrySelectionID === id} onSelect={onSelect} onSummary={report} />
    </div>)}</div>
    <p className="variant-note">Перед выбором маршрут будет обновлён. Чтение карточек не меняет план и не покупает билеты.</p>
  </>;
}
