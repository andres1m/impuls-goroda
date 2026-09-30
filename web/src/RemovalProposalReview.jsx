import React, { useEffect, useRef } from 'react';
import { userMessage } from './messages.js';

function time(value, timezone) {
  return new Intl.DateTimeFormat('ru-RU', { timeZone: timezone, hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}

function PlanSummary({ title, plan }) {
  const cost = plan.cost?.known_personal;
  const amount = cost?.currency === 'RUB' && /^\d+$/.test(cost.amount_minor || '') ? BigInt(cost.amount_minor) : null;
  return <section className="proposal-plan-summary">
    <h3>{title}</h3>
    {amount !== null && <p>{(amount / 100n).toLocaleString('ru-RU')}{amount % 100n ? `,${String(amount % 100n).padStart(2, '0')}` : ''} ₽{plan.cost.unknown_components?.length ? ' + неизвестные расходы' : ''}</p>}
    <ol>{plan.steps.map((step) => <li key={step.visit_id}>
      <span>{time(step.visit_start_at, plan.timezone)}—{time(step.visit_end_at, plan.timezone)}</span>
      <strong>{step.kind === 'free_time' ? 'Свободное время' : step.external_venue?.title || step.catalog?.title || 'Посещение'}</strong>
    </li>)}</ol>
  </section>;
}

export default function RemovalProposalReview({ route, proposal, disabled, onApply, onReject }) {
  const heading = useRef(null);
  useEffect(() => { heading.current?.focus(); }, [proposal.proposal_id]);
  const before = new Map(route.plan.steps.map((step) => [step.visit_id, step.external_venue?.title || step.catalog?.title || 'Свободное время']));
  const after = new Map(proposal.candidate.steps.map((step) => [step.visit_id, step.external_venue?.title || step.catalog?.title || 'Свободное время']));
  return <section className="removal-proposal-review" aria-labelledby="removal-proposal-heading">
    <h2 id="removal-proposal-heading" tabIndex={-1} ref={heading}>{proposal.reason === 'cancel' ? 'Маршрут после отмены' : 'Предложение маршрута'}</h2>
    <p>Изменения ещё не применены.</p>
    <div className="proposal-plan-columns"><PlanSummary title="Сейчас" plan={route.plan} /><PlanSummary title="После изменения" plan={proposal.candidate} /></div>
    <ul className="proposal-change-list">{proposal.changes.map((change, index) => <li key={index}>
      {change.kind === 'removed' ? `Убрать: ${before.get(change.before_visit_id) || 'посещение'}` : change.kind === 'replaced' ? `${before.get(change.before_visit_id) || 'Посещение'} → ${after.get(change.after_visit_id) || 'новая точка'}` : change.kind === 'participation_action' ? proposal.reason === 'cancel' ? 'Уточните возврат или перенос у организатора. Применение маршрута не возвращает деньги и не отменяет регистрацию.' : 'Билет или регистрация сохраняются. Проверьте условия у организатора.' : change.kind === 'cost_changed' ? 'Расходы изменятся — сравните суммы выше.' : change.kind === 'time_shifted' ? 'Время посещения изменится.' : 'Расписание или условия посещения обновятся.'}
    </li>)}</ul>
    {proposal.candidate.result === 'PARTIAL' && <p className="scenario-error">Вариант с оговорками — проверьте условия.</p>}
    {proposal.candidate.warnings?.map((warning, index) => <p key={index}>{userMessage(warning)}</p>)}
    <div className="server-participation-actions">
      <button className="scenario-option scenario-primary" disabled={disabled} onClick={onApply}>Применить</button>
      <button className="scenario-option" disabled={disabled} onClick={onReject}>Отклонить</button>
    </div>
  </section>;
}
