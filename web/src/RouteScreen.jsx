import React from 'react';

const cityNames = { moscow: 'Москва', perm: 'Пермь' };
const categoryNames = {
  culture: 'Культура', sport: 'Спорт', volunteer: 'Волонтёрство',
  walk: 'Прогулка', tourism: 'Туризм', gastro: 'Еда',
};
const availabilityNames = {
  registration_required: 'Нужна регистрация', sold_out: 'Мест нет',
  cancelled: 'Отменено', unknown: 'Доступность неизвестна',
};

function localTime(value, timezone, options) {
  try {
    return new Intl.DateTimeFormat('ru-RU', { timeZone: timezone, ...options }).format(new Date(value));
  } catch {
    return 'Время не указано';
  }
}

function money(value) {
  if (!value || !/^\d+$/.test(value.amount_minor || '')) return null;
  if (value.currency !== 'RUB') return `${value.amount_minor} ${value.currency} в минимальных единицах`;
  const minor = BigInt(value.amount_minor);
  return `${(minor / 100n).toLocaleString('ru-RU')}${minor % 100n ? `,${String(minor % 100n).padStart(2, '0')}` : ''} ₽`;
}

export default function RouteScreen({ route }) {
  const { plan } = route;
  const timezone = plan.timezone;
  const visits = plan.steps.filter((step) => step.kind === 'visit');
  const date = localTime(plan.start_at, timezone, { day: 'numeric', month: 'long', weekday: 'long' });
  const start = localTime(plan.start_at, timezone, { hour: '2-digit', minute: '2-digit' });
  const end = localTime(plan.end_at, timezone, { hour: '2-digit', minute: '2-digit' });
  const personalCost = money(plan.cost?.known_personal);
  const unknownCost = (plan.cost?.unknown_components?.length || 0) > 0;

  return (
    <div className="route-page">
      <header className="route-heading">
        <div>
          <span className="route-eyebrow">ИМПУЛЬС ГОРОДА · {cityNames[route.city] || route.city}</span>
          <h1>Ваш маршрут</h1>
          <p>{date} · {start}–{end}</p>
        </div>
        <span className="route-lifecycle">{route.lifecycle === 'saved' ? 'Сохранён' : 'Черновик'}</span>
      </header>

      <div className="route-overview" aria-label="Сводка маршрута">
        <div><strong>{visits.length}</strong><span>точек</span></div>
        <div><strong>{start}–{end}</strong><span>местное время · {timezone}</span></div>
        <div><strong>{personalCost ? `${unknownCost ? 'Известно: ' : ''}${personalCost}` : 'Неизвестно'}</strong><span>{unknownCost ? 'Есть неучтённые расходы' : 'Известные личные расходы'}</span></div>
      </div>

      {plan.result === 'PARTIAL' && <p className="route-notice">Вариант построен с оговорками. Проверьте условия посещений.</p>}
      {plan.warnings?.map((warning, index) => <p className="route-notice" key={`${warning.code}-${index}`}>{warning.message}</p>)}

      <section className="route-itinerary" aria-labelledby="itinerary-title">
        <div className="route-section-heading">
          <h2 id="itinerary-title">Расписание</h2>
          <span>{timezone}</span>
        </div>
        {plan.steps.length === 0 ? <p className="route-empty-steps">В этом маршруте пока нет посещений.</p> : (
          <ol className="route-steps">
            {plan.steps.map((step) => {
              const isVisit = step.kind === 'visit';
              const title = isVisit ? step.catalog?.title : 'Свободное время';
              return (
                <li className="route-step" key={step.visit_id}>
                  <div className="route-time">{localTime(step.visit_start_at, timezone, { hour: '2-digit', minute: '2-digit' })}</div>
                  <div className="route-marker" aria-hidden="true">{step.position}</div>
                  <div className="route-step-body">
                    <div className="route-step-topline">
                      <span>{isVisit ? categoryNames[step.catalog?.category] || 'Посещение' : 'Пауза'}</span>
                      <span>{localTime(step.visit_end_at, timezone, { hour: '2-digit', minute: '2-digit' })}</span>
                    </div>
                    <h3>{title || 'Название не указано'}</h3>
                    {isVisit && availabilityNames[step.catalog?.availability] && <p className="route-caution">{availabilityNames[step.catalog.availability]}</p>}
                    {isVisit && step.catalog?.data_mode && step.catalog.data_mode !== 'live' && <p className="route-source">Данные: {step.catalog.data_mode === 'prepared' ? 'подготовленные' : 'синтетические'}</p>}
                    {isVisit && step.cost?.unknown_components?.length > 0 && <p className="route-source">Стоимость уточняется</p>}
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </section>
    </div>
  );
}
