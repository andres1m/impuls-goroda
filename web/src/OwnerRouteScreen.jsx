import React, { useEffect, useRef, useState } from 'react';
import RouteScreen from './RouteScreen.jsx';
import RemovalProposalReview from './RemovalProposalReview.jsx';
import PanicControls from './PanicControls.jsx';
import ShareControls from './ShareControls.jsx';
import LunchSearch from './LunchSearch.jsx';
import { createShareAttempt, createRevokeShareAttempt, sendShareCommand } from './sharing.js';
import { createPanicAttempt, createRemovalAttempt, createProposalResolutionAttempt, sendProposalCommand } from './proposalCommands.js';
import { loadOwnerRoute } from './route.js';
import { createDeleteAttempt, createExecutionAttempt, createParticipationAttempt, createPinAttempt, createRouteAttempt, sendRouteCommand, terminalRouteError } from './routeCommands.js';

export default function OwnerRouteScreen({ route: initialRoute, apiBaseUrl, accessToken, mapApiKey, onBack }) {
  const [route, setRoute] = useState(initialRoute);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState('');
  const [proposal, setProposal] = useState(initialRoute.pending_proposal || null);
  const [shareLink, setShareLink] = useState(null);
  const [deleteReview, setDeleteReview] = useState(false);
  const [deleteAcknowledged, setDeleteAcknowledged] = useState(false);
  const [deleted, setDeleted] = useState(false);
  const command = useRef(null);
  const request = useRef(null);
  useEffect(() => () => request.current?.abort(), []);

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
          setMessage(result.conflicts.map((item) => item.message).join(' ') || 'Действие отклонено из-за конфликта условий.');
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

  if (deleted) return <main className="entry-page"><section className="entry-state" role="status"><h1>Маршрут удалён</h1><p>Билеты и регистрации не отменены. Независимые копии маршрута сохранятся.</p>{onBack && <button className="scenario-option" onClick={onBack}>К вариантам</button>}</section></main>;

  return <>
    {onBack && <button className="scenario-return" disabled={busy || pending || Boolean(proposal)} onClick={onBack}>К вариантам</button>}
    {proposal && <RemovalProposalReview route={route} proposal={proposal} disabled={busy || pending} onApply={() => runCommand('apply')} onReject={() => runCommand('reject')} />}
    <PanicControls route={route} mapApiKey={mapApiKey} disabled={busy || pending || Boolean(proposal)} onPanic={(input) => runCommand('panic', input)} />
    <RouteScreen route={route} mapApiKey={mapApiKey} actionsDisabled={busy || pending || Boolean(proposal)} onExecution={(visitID, status, times) => runCommand('execution', visitID, status, times)} onParticipation={(visitID, action) => runCommand('participation', visitID, action)} onPin={(visitID, kind) => runCommand('pin', visitID, kind)} onRemoval={(visitID, mode, acknowledge) => runCommand('removal', visitID, mode, acknowledge)} />
    <LunchSearch key={route.route_id} routeID={route.route_id} apiBaseUrl={apiBaseUrl} accessToken={accessToken} disabled={busy || pending} />
    <section className="owner-route-actions" aria-label="Сохранение маршрута">
      <button className="scenario-option scenario-primary" disabled={busy || Boolean(proposal) && !pending || route.lifecycle === 'saved' && !pending} onClick={() => runCommand()}>
        {busy ? 'Выполняем…' : command.current?.conflict ? 'Обновить маршрут' : pending ? 'Повторить запрос' : route.lifecycle === 'saved' ? 'Сохранён' : 'Сохранить'}
      </button>
      {message && <p role="status">{message}</p>}
    </section>
    <ShareControls key={shareLink || 'no-share-link'} link={shareLink} disabled={busy || pending || Boolean(proposal)} onCreate={() => runCommand('share-create')} onRevoke={() => runCommand('share-revoke')} />
    <section className="owner-route-actions" aria-label="Удаление маршрута">
      {!deleteReview ? <button className="scenario-option" disabled={busy || pending || Boolean(proposal)} onClick={() => setDeleteReview(true)}>Удалить маршрут</button> : <>
        <h2>Удалить весь маршрут?</h2>
        <p>Ссылка перестанет работать. Билеты и регистрации не отменятся; независимые копии сохранятся.</p>
        <label className="route-delete-ack"><input type="checkbox" checked={deleteAcknowledged} disabled={busy || pending} onChange={(event) => setDeleteAcknowledged(event.target.checked)} />Я понимаю, что билеты и регистрации нужно отменять отдельно</label>
        <button className="scenario-option" disabled={busy || pending || !deleteAcknowledged} onClick={() => runCommand('delete')}>Удалить без восстановления</button>
        <button className="scenario-option" disabled={busy || pending} onClick={() => { setDeleteReview(false); setDeleteAcknowledged(false); }}>Оставить маршрут</button>
      </>}
    </section>
  </>;
}
