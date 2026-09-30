import React, { useEffect, useRef, useState } from 'react';
import RouteScreen from './RouteScreen.jsx';
import RemovalProposalReview from './RemovalProposalReview.jsx';
import PanicControls from './PanicControls.jsx';
import ShareControls from './ShareControls.jsx';
import NotificationControls from './NotificationControls.jsx';
import LunchSearch from './LunchSearch.jsx';
import { localDateTime, scenarioLunchWindow } from './scenarioForm.js';
import { loadScenario, createScenario, createDraftAttempt, saveScenarioDraft, createCompletionAttempt, completeScenario } from './scenario.js';
import { archetypeTitles } from './routeProjection.js';
import { userMessage } from './messages.js';
import { createShareAttempt, createRevokeShareAttempt, sendShareCommand } from './sharing.js';
import { createPanicAttempt, createRemovalAttempt, createProposalResolutionAttempt, sendProposalCommand } from './proposalCommands.js';
import { loadOwnerRoute } from './route.js';
import { createDeleteAttempt, createExecutionAttempt, createParticipationAttempt, createPinAttempt, createRouteAttempt, sendRouteCommand, terminalRouteError } from './routeCommands.js';

export default function OwnerRouteScreen({ route: initialRoute, apiBaseUrl, accessToken, mapApiKey, onBack, backLabel = 'К вариантам', variantIDs = [] }) {
  const [route, setRoute] = useState(initialRoute);
  const [commandBusy, setBusy] = useState(false);
  const [notificationBlocked, setNotificationBlocked] = useState(false);
  const busy = commandBusy || notificationBlocked;
  const [refreshing, setRefreshing] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState('');
  const [proposal, setProposal] = useState(initialRoute.pending_proposal || null);
  const [shareLink, setShareLink] = useState(null);
  const [deleteReview, setDeleteReview] = useState(false);
  const [deleteAcknowledged, setDeleteAcknowledged] = useState(false);
  const [deleted, setDeleted] = useState(false);
  const [lunchRequest, setLunchRequest] = useState(0);
  const [lunchPreview, setLunchPreview] = useState(null);
  const [panicRequest, setPanicRequest] = useState(0);
  const [variants, setVariants] = useState([]);
  const [variantsOpen, setVariantsOpen] = useState(true);
  const scenario = useRef(null);
  const switching = useRef(null);
  const rebuilding = useRef(null);
  const command = useRef(null);
  const request = useRef(null);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => { setLunchPreview(null); }, [route.route_id, route.revision]);

  function chooseLunch(value) {
    setLunchPreview(value);
    requestAnimationFrame(() => document.querySelector('.workspace-map-column')?.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  }

  useEffect(() => {
    const controller = new AbortController();
    async function readVariants() {
      const latest = await loadScenario(apiBaseUrl, accessToken, null, fetch, controller.signal);
      scenario.current = latest?.outcome?.route_ids?.includes(initialRoute.route_id) ? latest : null;
      const ids = variantIDs.length ? variantIDs : latest?.outcome?.route_ids || [];
      if (!ids.includes(initialRoute.route_id)) return;
      const values = await Promise.all(ids.map((id) => loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal).catch(() => null)));
      if (!controller.signal.aborted) setVariants(values.filter(Boolean));
    }
    readVariants().catch(() => {});
    return () => controller.abort();
  }, [apiBaseUrl, accessToken, initialRoute.route_id, variantIDs.join(',')]);

  async function chooseVariant(id) {
    if (busy || command.current || proposal || deleteReview) return;
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setMessage('');
    try {
      if (!switching.current || switching.current.id !== id) {
        const value = await loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal);
        switching.current = { id, attempt: createRouteAttempt('selection', value), acknowledged: false };
      }
      const choice = switching.current;
      if (!choice.acknowledged) {
        const result = await sendRouteCommand(apiBaseUrl, accessToken, choice.attempt, fetch, controller.signal);
        if (result.status === 'CONFLICT') { setMessage(result.conflicts.map(userMessage).join(' ')); switching.current = null; return; }
        choice.acknowledged = true;
      }
      const value = await loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal);
      if (!controller.signal.aborted) { setRoute(value); setProposal(value.pending_proposal || null); setShareLink(null); switching.current = null; }
    } catch (error) {
      if (!controller.signal.aborted) {
        if (terminalRouteError(error)) switching.current = null;
        setMessage('Не удалось переключить вариант. Нажмите на него ещё раз.');
      }
    } finally { if (!controller.signal.aborted) { setBusy(false); request.current = null; } }
  }

  async function rebuild(lunchForm = null) {
    if (busy || pending || proposal || deleteReview || switching.current) return { ok: false, message: 'Дождитесь завершения текущего действия.' };
    let lunchWindow = null;
    if (lunchForm) {
      if (Date.parse(route.plan.start_at) < Date.now()) return { ok: false, message: 'Прошедший маршрут нельзя пересчитать с новым временем обеда. Создайте прогулку на будущую дату.' };
      try {
        lunchWindow = scenarioLunchWindow(route.plan, {
          lunchEnabled: true, start: localDateTime(route.plan.start_at, route.plan.timezone),
          end: localDateTime(route.plan.end_at, route.plan.timezone), ...lunchForm,
        }, route.plan.timezone);
      } catch (failure) { return { ok: false, message: failure.message }; }
    }
    if (rebuilding.current && (rebuilding.current.kind !== (lunchForm ? 'lunch' : 'variants') || lunchForm && JSON.stringify(rebuilding.current.lunchWindow) !== JSON.stringify(lunchWindow))) return { ok: false, message: 'Сначала повторите предыдущий расчёт с теми же условиями.' };
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setMessage('');
    try {
      const plan = route.plan;
      const source = scenario.current?.source === 'custom' && scenario.current.source_text
        ? { source_text: scenario.current.source_text } : { preset_id: scenario.current?.preset_id || 'balance' };
      if (!rebuilding.current) rebuilding.current = { source, key: crypto.randomUUID(), kind: lunchForm ? 'lunch' : 'variants', lunchWindow };
      const command = rebuilding.current;
      if (!command.created) command.created = await createScenario(apiBaseUrl, accessToken, command.source, command.key, controller.signal);
      if (!command.draft) {
        const input = { city: route.city, timezone: plan.timezone, start_at: plan.start_at, end_at: plan.end_at, origin: plan.origin, constraints: command.lunchWindow ? { ...plan.constraints, lunch_window: command.lunchWindow } : plan.constraints, ...(plan.destination ? { destination: plan.destination } : {}) };
        command.draft = createDraftAttempt(command.created, input);
      }
      if (!command.saved) command.saved = await saveScenarioDraft(apiBaseUrl, accessToken, command.draft, fetch, controller.signal);
      if (!command.completion) command.completion = createCompletionAttempt(command.saved, command.saved.input);
      const result = await completeScenario(apiBaseUrl, accessToken, command.completion, fetch, controller.signal);
      if (controller.signal.aborted) return;
      const latest = await loadScenario(apiBaseUrl, accessToken, command.created.scenario_id, fetch, controller.signal);
      if (controller.signal.aborted) return;
      scenario.current = latest;
      const values = await Promise.all((latest.outcome?.route_ids || []).map((id) => loadOwnerRoute(apiBaseUrl, accessToken, id, fetch, controller.signal)));
      if (controller.signal.aborted) return;
      const outcomeMessage = values.length ? command.kind === 'lunch' ? 'Новые варианты с заданным временем обеда готовы. Проверьте обед в расписании и выберите подходящий.' : 'Новые варианты готовы. Выберите подходящий.'
        : result.conflicts?.map(userMessage).join(' ') || 'С этими условиями другой маршрут не найден. Текущий маршрут сохранён.';
      if (values.length) { setVariants(values); setVariantsOpen(true); }
      setMessage(outcomeMessage);
      rebuilding.current = null;
      return { ok: values.length > 0, message: outcomeMessage };
    } catch (error) {
      if (!controller.signal.aborted) {
        if (terminalRouteError(error)) rebuilding.current = null;
        setMessage('Не удалось получить новые варианты. Текущий маршрут сохранён. Можно повторить.');
      }
      return { ok: false, message: 'Не удалось получить новые варианты. Текущий маршрут сохранён. Можно повторить.' };
    } finally { if (!controller.signal.aborted) { request.current = null; setBusy(false); } }
  }

  async function refreshRoute() {
    if (command.current || deleteReview || request.current && !request.current.signal.aborted) return;
    const controller = new AbortController();
    request.current = controller;
    setBusy(true); setRefreshing(true); setMessage('');
    try {
      const updated = await loadOwnerRoute(apiBaseUrl, accessToken, route.route_id, fetch, controller.signal);
      if (controller.signal.aborted) return;
      if (BigInt(updated.revision) < BigInt(route.revision)) throw new Error('Stale route snapshot');
      setRoute(updated);
      setProposal(updated.pending_proposal || null);
      setMessage('Маршрут обновлён.');
    } catch (error) {
      if (controller.signal.aborted) return;
      setMessage(error.status === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.'
        : error.status === 404 ? 'Маршрут больше недоступен.' : 'Не удалось обновить маршрут. Прежний план сохранён на экране. Попробуйте ещё раз.');
    } finally {
      if (!controller.signal.aborted) { request.current = null; setBusy(false); setRefreshing(false); }
    }
  }

  async function runCommand(operation = 'save', visitID, status, acknowledge = false) {
    if (request.current && !request.current.signal.aborted) return;
    const controller = new AbortController();
    request.current = controller;
    setBusy(true); setMessage('');
    try {
      if (!command.current) {
        const attempt = operation === 'execution' ? createExecutionAttempt(route, visitID, status, undefined, acknowledge)
          : operation === 'participation' ? createParticipationAttempt(route, visitID, status)
          : operation === 'pin' ? createPinAttempt(route, visitID, status)
          : operation === 'removal' ? createRemovalAttempt(route, visitID, status, acknowledge)
          : operation === 'panic' ? createPanicAttempt(route, visitID)
          : operation === 'share-create' ? createShareAttempt(route)
          : operation === 'share-revoke' ? createRevokeShareAttempt(route)
          : operation === 'delete' ? createDeleteAttempt(route, deleteAcknowledged)
          : ['apply', 'reject'].includes(operation) ? createProposalResolutionAttempt(operation, route, proposal) : createRouteAttempt('save', route);
        command.current = { attempt, acknowledged: false, conflict: false };
      }
      const value = command.current;
      if (!value.acknowledged && !value.conflict) {
        const send = ['create', 'revoke'].includes(value.attempt.operation) ? sendShareCommand
          : ['removal', 'panic', 'apply', 'reject'].includes(value.attempt.operation) ? sendProposalCommand : sendRouteCommand;
        const result = await send(apiBaseUrl, accessToken, value.attempt, fetch, controller.signal);
        if (controller.signal.aborted) return;
        if (result.status === 'CONFLICT') {
          command.current = null; setPending(false);
          setMessage(result.conflicts.map(userMessage).join(' ') || 'Действие отклонено из-за конфликта условий.');
          return;
        }
        value.acknowledged = true;
        value.result = result;
      }
      if (value.attempt.operation === 'delete' && value.acknowledged && !value.conflict) {
        command.current = null; setPending(false); setShareLink(null); setProposal(null); setRoute(null); setDeleted(true);
        return;
      }
      if (['create', 'revoke'].includes(value.attempt.operation) && !value.conflict) {
        setShareLink(value.attempt.operation === 'create' ? value.result.deep_link : null);
        command.current = null; setPending(false);
        setMessage(value.attempt.operation === 'create' ? 'Ссылка готова.' : 'Ссылка отозвана. Созданные по ней копии сохранятся.');
        return;
      }
      if (['removal', 'panic'].includes(value.attempt.operation) && value.result?.status === 'PROPOSED' && !value.conflict) {
        setProposal(value.result.proposal); command.current = null; setPending(false);
        return;
      }
      const updated = await loadOwnerRoute(apiBaseUrl, accessToken, value.attempt.routeID, fetch, controller.signal);
      if (controller.signal.aborted) return;
      if (value.acknowledged && value.attempt.operation === 'save' && updated.lifecycle !== 'saved') throw new Error('Invalid saved route');
      if (value.acknowledged && ['pin', 'apply', 'reject', 'removal', 'panic'].includes(value.attempt.operation) && BigInt(updated.revision) < BigInt(value.result.revision)) throw new Error('Stale route snapshot');
      setRoute(updated); command.current = null; setPending(false); setDeleteReview(false); setDeleteAcknowledged(false);
      setProposal(updated.pending_proposal || null);
      const providerConfirmed = value.result?.execution?.confirmation_kind === 'provider_confirmed' || value.result?.participation?.evidence === 'provider';
      setMessage(value.conflict ? 'Маршрут обновлён. Проверьте его и повторите нужное действие.' : value.attempt.operation === 'apply' ? 'Изменения применены.' : value.attempt.operation === 'reject' ? 'Предложение отклонено. Маршрут не изменён.' : ['removal', 'panic'].includes(value.attempt.operation) ? 'Маршрут не изменился.' : value.attempt.operation === 'save' ? 'Маршрут сохранён.' : providerConfirmed ? 'Подтверждение источника сохранено.' : 'Отметка сохранена.');
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error.code === 'REVISION_CONFLICT') {
        command.current.conflict = true;
        setPending(true); setMessage('Маршрут изменился. Обновите его перед повторным действием.');
      } else if (terminalRouteError(error)) {
        command.current = null; setPending(false);
        if (['PROPOSAL_NOT_PENDING', 'PROPOSAL_CATALOG_CHANGED'].includes(error.code)) setProposal(null);
        setMessage(error.code === 'PROPOSAL_CATALOG_CHANGED' ? 'Данные обновились. Запросите новое предложение.' : error.code === 'PROPOSAL_NOT_PENDING' ? 'Предложение больше недоступно. Запросите новое.' : error.code === 'EXTERNAL_COMMITMENT_CONFIRMATION_REQUIRED' ? 'Подтвердите, что удаление не отменяет билет или регистрацию.' : error.status === 404 ? 'Маршрут или посещение больше недоступны.' : 'Не удалось выполнить действие. Проверьте маршрут.');
      } else {
        setPending(command.current !== null);
        setMessage(error.status === 401 ? 'Сессия закончилась. Откройте Mini App заново в MAX.' : 'Не удалось получить ответ. Повторите запрос.');
      }
    } finally {
      if (!controller.signal.aborted) { request.current = null; setBusy(false); }
    }
  }

  if (deleted) return <main className="entry-page"><section className="entry-state" role="status"><h1>Маршрут удалён</h1><p>Билеты и регистрации не отменены. Независимые копии маршрута сохранятся.</p>{onBack && <button className="scenario-option" onClick={onBack}>{backLabel}</button>}</section></main>;

  return <div className="owner-page">
    <RouteScreen route={route} mapApiKey={mapApiKey} apiBaseUrl={apiBaseUrl} lunchPreview={lunchPreview} onClearLunch={() => setLunchPreview(null)}
      toolbar={<><nav className="owner-toolbar">{onBack && <button disabled={busy || pending || Boolean(proposal)} onClick={onBack}>{backLabel}</button>}<button disabled={busy || pending || deleteReview} onClick={refreshRoute}>{refreshing ? 'Обновляем…' : 'Обновить'}</button></nav>
        {proposal && <RemovalProposalReview route={route} proposal={proposal} disabled={busy || pending} onApply={() => runCommand('apply')} onReject={() => runCommand('reject')} />}
        <PanicControls openRequest={panicRequest} hideLauncher route={route} mapApiKey={mapApiKey} disabled={busy || pending || Boolean(proposal)} onPanic={(input) => runCommand('panic', input)} /></>}
      variantTabs={variantsOpen && <section className="owner-variants" aria-label="Варианты маршрута">{variants.map((value) => <button key={value.route_id} aria-pressed={route.route_id === value.route_id} disabled={busy || pending || Boolean(proposal)} onClick={() => chooseVariant(value.route_id)}>{archetypeTitles[value.plan.archetype_id] || 'Вариант маршрута'}</button>)}<button disabled={busy || pending || Boolean(proposal)} onClick={() => rebuild()}>{rebuilding.current ? 'Повторить расчёт' : 'Другие варианты'}</button></section>}
      actionsDisabled={busy || pending || Boolean(proposal)} lunchSearchDisabled={busy || pending} onLunch={() => setLunchRequest((value) => value + 1)} onExecution={(visitID, status, times) => runCommand('execution', visitID, status, times)} onParticipation={(visitID, action) => runCommand('participation', visitID, action)} onPin={(visitID, kind) => runCommand('pin', visitID, kind)} onRemoval={(visitID, mode, acknowledge) => runCommand('removal', visitID, mode, acknowledge)} />
    <LunchSearch hideLauncher key={`lunch-${route.route_id}`} openRequest={lunchRequest} routeID={route.route_id} city={route.city} plan={route.plan} apiBaseUrl={apiBaseUrl} accessToken={accessToken} mapApiKey={mapApiKey} disabled={busy || pending} onChoose={chooseLunch} onSchedule={rebuild} />
    <section className="owner-fixed-actions" aria-label="Действия маршрута">
      <button className="scenario-option" disabled={busy || pending || Boolean(proposal)} onClick={() => { setVariantsOpen(true); document.querySelector('.owner-variants')?.scrollIntoView({ behavior: 'smooth', block: 'center' }); }}>Варианты</button>
      <button className="scenario-option" disabled={busy || pending} onClick={() => setLunchRequest((value) => value + 1)}>Обед</button>
      <button className="scenario-option" disabled={busy || pending || Boolean(proposal)} onClick={() => { setPanicRequest((value) => value + 1); }}>Опаздываю</button>
      <button className="scenario-option scenario-primary" disabled={busy || Boolean(proposal) && !pending || route.lifecycle === 'saved' && !pending} onClick={() => runCommand()}>
        {busy ? 'Выполняем…' : command.current?.conflict ? 'Обновить маршрут' : pending ? 'Повторить запрос' : route.lifecycle === 'saved' ? 'Сохранён' : 'Сохранить'}
      </button>
    </section>
    {message && <p className="owner-message" role="status">{message}</p>}
    <ShareControls key={shareLink || 'no-share-link'} link={shareLink} disabled={busy || pending || Boolean(proposal)} onCreate={() => runCommand('share-create')} onRevoke={() => runCommand('share-revoke')} />
    {route.lifecycle === 'saved' && <NotificationControls key={`notifications-${route.route_id}`} route={route} apiBaseUrl={apiBaseUrl} accessToken={accessToken}
      disabled={commandBusy || pending || Boolean(proposal) || deleteReview} onBlockingChange={setNotificationBlocked}
      onRouteUpdated={(updated) => { setRoute(updated); setProposal(updated.pending_proposal || null); }} />}
    <section className="owner-route-actions" aria-label="Удаление маршрута">
      {!deleteReview ? <button className="scenario-option" disabled={busy || pending || Boolean(proposal)} onClick={() => setDeleteReview(true)}>Удалить маршрут</button> : <>
        <h2>Удалить весь маршрут?</h2>
        <p>Ссылка перестанет работать. Билеты и регистрации не отменятся; независимые копии сохранятся.</p>
        <label className="route-delete-ack"><input type="checkbox" checked={deleteAcknowledged} disabled={busy || pending} onChange={(event) => setDeleteAcknowledged(event.target.checked)} />Я понимаю, что билеты и регистрации нужно отменять отдельно</label>
        <button className="scenario-option" disabled={busy || pending || !deleteAcknowledged} onClick={() => runCommand('delete')}>Удалить без восстановления</button>
        <button className="scenario-option" disabled={busy || pending} onClick={() => { setDeleteReview(false); setDeleteAcknowledged(false); }}>Оставить маршрут</button>
      </>}
    </section>
  </div>;
}
