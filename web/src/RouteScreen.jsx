import React, { useMemo, useRef, useState } from 'react';
import Brand from './Brand.jsx';
import ServerRouteMap from './ServerRouteMap.jsx';
import { pinHistoryIncomplete } from './routeCommands.js';
import { archetypeTitles, categoryTitles, projectRoute, travelTitles, verificationTitles } from './routeProjection.js';

const availabilityNames = { available: 'Доступно по данным каталога', registration_required: 'Нужна регистрация', sold_out: 'Мест нет', cancelled: 'Отменено', unknown: 'Доступность неизвестна' };
const dataNames = { live: 'Актуальные данные', prepared: 'Подготовленные данные', synthetic: 'Синтетические данные' };
const participationNames = { not_required: 'Оформление не требуется', action_required: 'Нужно оформить участие', user_reported_confirmed: 'Участие оформлено · ваша отметка', provider_confirmed: 'Участие подтверждено источником', unavailable: 'Участие недоступно' };
function localTime(value, timezone, options = { hour: '2-digit', minute: '2-digit' }) {
  try { return new Intl.DateTimeFormat('ru-RU', { timeZone: timezone, ...options }).format(new Date(value)); }
  catch { return 'Время не указано'; }
}
function money(value) {
  if (!value || !/^\d+$/.test(value.amount_minor || '')) return null;
  if (value.currency !== 'RUB') return `${value.amount_minor} ${value.currency} в минимальных единицах`;
  const minor = BigInt(value.amount_minor);
  return `${(minor / 100n).toLocaleString('ru-RU')}${minor % 100n ? `,${String(minor % 100n).padStart(2, '0')}` : ''} ₽`;
}
function Leg({ leg, timezone }) {
  const duration = (Date.parse(leg.arrival_at) - Date.parse(leg.departure_at)) / 60000;
  return <li className={`server-leg is-${leg.verification}`}>
    <span>{travelTitles[leg.mode] || leg.mode} · {Number.isFinite(duration) && duration >= 0 ? `${Math.ceil(duration)} мин.` : 'время неизвестно'}{Number.isFinite(leg.distance_meters) ? ` · ${Math.round(leg.distance_meters)} м` : ''}</span>
    <small>{localTime(leg.departure_at, timezone)}—{localTime(leg.arrival_at, timezone)} · {verificationTitles[leg.verification] || 'Путь не проверен'}</small>
  </li>;
}

function RemovalControls({ step, participation, disabled, onRemove }) {
  const [open, setOpen] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const external = ['user_reported_confirmed', 'provider_confirmed'].includes(participation?.status);
  return <section className="server-participation" aria-label="Удаление точки">
    {!open ? <button className="scenario-option" disabled={disabled} onClick={() => setOpen(true)}>Убрать из маршрута</button> : <>
      <h3>Как использовать это время?</h3>
      {external && <label className="removal-acknowledgement"><input type="checkbox" checked={acknowledged} disabled={disabled} onChange={(event) => setAcknowledged(event.target.checked)} />Понимаю, что билет или регистрация не отменятся</label>}
      <div className="server-participation-actions">
        <button className="scenario-option" disabled={disabled || external && !acknowledged} onClick={() => onRemove(step.visit_id, 'rebuild', acknowledged)}>Перестроить окно</button>
        <button className="scenario-option" disabled={disabled || external && !acknowledged} onClick={() => onRemove(step.visit_id, 'free_time', acknowledged)}>Оставить паузу</button>
      </div>
      <button className="scenario-return" disabled={disabled} onClick={() => setOpen(false)}>Не удалять</button>
    </>}
  </section>;
}

export default function RouteScreen({ route, mapApiKey, shared = false, actionsDisabled = false, onExecution, onParticipation, onPin, onRemoval }) {
  const { plan } = route;
  const projection = useMemo(() => projectRoute(route), [route]);
  const [selectedID, setSelectedID] = useState(null);
  const cards = useRef(new Map());
  const timezone = plan.timezone;
  const start = localTime(plan.start_at, timezone);
  const end = localTime(plan.end_at, timezone);
  const personalCost = money(plan.cost?.known_personal);
  const unknownCost = (plan.cost?.unknown_components?.length || 0) > 0;
  const mappedLegs = new Set();
  const pinUnavailable = pinHistoryIncomplete(route);
  function chooseVisit(id) { setSelectedID(id); cards.current.get(id)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); }
  return <main className="workspace server-route-workspace" style={{ '--route-accent': '#087af5' }}>
    <header className="workspace-topbar"><Brand className="workspace-wordmark" /><span className="server-lifecycle">{shared ? 'Общий маршрут' : route.lifecycle === 'saved' ? 'Сохранён' : 'Черновик'}</span></header>
    <div className="workspace-wrap">
      <div className="workspace-heading"><div className="workspace-title-block">
        <h1>{archetypeTitles[plan.archetype_id] || 'Ваш маршрут'}</h1>
        <p>{localTime(plan.start_at, timezone, { day: 'numeric', month: 'long' })} · {start}—{end} · {projection.visits.length} точек</p>
      </div></div>
      <div className="server-summary"><span>{personalCost ? `${unknownCost ? 'Известные расходы: ' : ''}${personalCost}` : 'Стоимость неизвестна'}</span>{unknownCost && <span>Есть неучтённые расходы</span>}{plan.constraints?.pushkin_card_only && <span>По Пушкинской карте</span>}</div>
      {plan.result === 'PARTIAL' && <p className="workspace-route-alert is-warning">Вариант построен с оговорками. Проверьте условия посещений.</p>}
      {plan.warnings?.map((warning, index) => <p className="workspace-route-alert is-warning" key={`${warning.code}-${index}`}>{warning.message}</p>)}
      {route.issues?.filter((issue) => issue.state !== 'resolved').map((issue) => <p className="workspace-route-alert is-warning" key={issue.issue_id}>{issue.message}</p>)}
      <div className="workspace-columns">
        <ServerRouteMap apiKey={mapApiKey} projection={projection} onSelect={chooseVisit} />
        <section className="workspace-itinerary" aria-labelledby="server-itinerary-title">
          <div className="workspace-section-head"><h2 id="server-itinerary-title">По пути</h2><span>Версия {route.revision}</span></div>
          <p className="server-local-time">{timezone} · местное время</p>
          {!projection.steps.length && <p>В этом маршруте пока нет посещений.</p>}
          <ol className="workspace-timeline">
            {projection.steps.map((step) => {
              const isVisit = step.kind === 'visit';
              const visit = projection.visits.find((item) => item.id === step.visit_id);
              const executed = projection.execution.get(step.visit_id);
              const participation = route.participation?.find((item) => item.visit_id === step.visit_id) || step.participation;
              const incoming = projection.legs.filter((leg) => leg.to_visit_id === step.visit_id);
              incoming.forEach((leg) => mappedLegs.add(leg.position));
              const selected = selectedID === step.visit_id;
              return <React.Fragment key={step.visit_id}>
                {incoming.map((leg) => <Leg key={leg.position} leg={leg} timezone={timezone} />)}
                <li ref={(node) => { if (node) cards.current.set(step.visit_id, node); else cards.current.delete(step.visit_id); }}>
                  <button className={isVisit ? `workspace-visit${selected ? ' is-active' : ''}${visit?.completed ? ' is-completed' : ''}` : 'workspace-lunch-card server-free-time'} aria-expanded={selected} onClick={() => setSelectedID(selected ? null : step.visit_id)}>
                    <span className="workspace-visit-time">{localTime(step.visit_start_at, timezone)}</span>
                    <span className="workspace-visit-main"><small>{isVisit && <span className="workspace-visit-marker" aria-hidden="true">{visit.completed ? '✓' : visit.number}</span>}{isVisit ? categoryTitles[step.catalog?.category] || 'Посещение' : 'Пауза'} · до {localTime(step.visit_end_at, timezone)}</small>
                      <strong>{isVisit ? visit.title : 'Свободное время'}</strong>
                      {visit?.completed ? <em>Пройдено{executed.confirmation_kind === 'user_reported' ? ' · по вашей отметке' : ' · подтверждено источником'}</em> : executed?.status === 'skipped' ? <em>Пропущено</em> : step.obligation ? <em>Обязательное посещение</em> : step.pinned ? <em>Закреплено</em> : null}
                    </span><span className="workspace-visit-chevron" aria-hidden="true">{selected ? '−' : '+'}</span>
                  </button>
                  {selected && <div className="server-visit-details">
                    <p>Прибытие {localTime(step.arrival_at, timezone)} · выход {localTime(step.departure_at, timezone)}</p>
                    {isVisit && <>
                      <p>{availabilityNames[step.catalog?.availability] || 'Доступность неизвестна'}</p>
                      <p>{dataNames[step.catalog?.data_mode] || 'Режим данных не указан'}</p>
                      <p>{step.cost?.unknown_components?.length ? 'Есть расходы, которые нужно уточнить' : money(step.cost?.personal_amount) || 'Личная стоимость неизвестна'}</p>
                      {step.catalog?.registration_details && <p>{step.catalog.registration_details}</p>}
                      {step.catalog?.age_requirements && <p>{step.catalog.age_requirements}</p>}
                      {!visit.point && <p>Координаты этой точки в плане отсутствуют. Она показана только в расписании.</p>}
                      {onPin && <section className="server-participation" aria-label="Закрепление посещения">
                        <h3>В маршруте</h3>
                        <p>{step.obligation ? 'Обязательное посещение' : step.pinned ? 'Предпочтительная точка' : 'Без закрепления'}</p>
                        {['completed', 'skipped'].includes(executed?.status) ? <p>История посещения сохраняется.</p> : <>
                          <div className="server-participation-actions">
                            <button className="scenario-option" disabled={actionsDisabled || pinUnavailable || step.pinned && !step.obligation} onClick={() => onPin(step.visit_id, 'preferred')}>Предпочесть</button>
                            <button className="scenario-option" disabled={actionsDisabled || pinUnavailable || step.pinned && step.obligation} onClick={() => onPin(step.visit_id, 'obligation')}>Обязательно</button>
                            <button className="scenario-option" disabled={actionsDisabled || pinUnavailable || !step.pinned && !step.obligation} onClick={() => onPin(step.visit_id, 'none')}>Снять закрепление</button>
                          </div>
                          {['user_reported_confirmed', 'provider_confirmed'].includes(participation?.status) && <p>Снятие закрепления сохраняет оформленное участие и обязательство.</p>}
                          {pinUnavailable && <p>Пересчёт пока недоступен: у пройденных точек не указано фактическое время.</p>}
                        </>}
                      </section>}
                      {!shared && <section className="server-participation" aria-label="Участие">
                        <h3>Участие</h3><p>{participationNames[participation?.status] || 'Статус участия не указан'}{participation?.status === 'unavailable' && participation.evidence === 'user' ? ' · ваша отметка' : ''}</p>
                        {participation?.evidence !== 'provider' && onParticipation && <>
                          <p className="server-participation-hint">Ваша отметка об оформлении, без проверки оплаты.</p>
                          <div className="server-participation-actions">
                            <button className="scenario-option" disabled={actionsDisabled || participation?.status === 'user_reported_confirmed'} onClick={() => onParticipation(step.visit_id, 'user_reported_confirmed')}>Я оформил участие</button>
                            <button className="scenario-option" disabled={actionsDisabled || participation?.status === 'unavailable' && participation.evidence === 'user'} onClick={() => onParticipation(step.visit_id, 'user_reported_unavailable')}>Не удалось оформить</button>
                            {participation?.evidence === 'user' && <button className="scenario-option" disabled={actionsDisabled} onClick={() => onParticipation(step.visit_id, 'clear_user_report')}>Снять мою отметку</button>}
                          </div>
                        </>}
                      </section>}
                      {executed?.confirmation_kind === 'provider_confirmed' ? <p>Выполнение подтверждено источником.</p> : onExecution && <div className="server-execution-actions" aria-label="Отметка посещения">
                        <button className="scenario-option" disabled={actionsDisabled || executed?.status === 'completed'} onClick={() => onExecution(step.visit_id, 'completed')}>{executed?.status === 'completed' ? 'Пройдено' : 'Отметить пройденным'}</button>
                        <button className="scenario-option" disabled={actionsDisabled || executed?.status === 'skipped'} onClick={() => onExecution(step.visit_id, 'skipped')}>{executed?.status === 'skipped' ? 'Пропущено' : 'Пропустить'}</button>
                      </div>}
                    </>}
                    {onRemoval && !['completed', 'skipped'].includes(executed?.status) && <RemovalControls step={step} participation={participation} disabled={actionsDisabled || pinUnavailable} onRemove={onRemoval} />}
                    {!isVisit && onRemoval && pinUnavailable && <p>Пересчёт пока недоступен: у пройденных точек не указано фактическое время.</p>}
                    {step.applied_constraints?.map((item, index) => <p key={`${item.code}-${index}`}>{item.message}</p>)}
                  </div>}
                </li>
              </React.Fragment>;
            })}
            {projection.legs.filter((leg) => !mappedLegs.has(leg.position)).map((leg) => <Leg key={`edge-${leg.position}`} leg={leg} timezone={timezone} />)}
          </ol>
        </section>
      </div>
    </div>
  </main>;
}
