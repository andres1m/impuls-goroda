const messages = {
  LUNCH_NO_VENUE: 'Подходящее кафе рядом с маршрутом не найдено. Время на обед оставлено свободным — можно поискать рядом с вами.',
  LUNCH_WINDOW: 'Время на обед',
  TRANSPORT_FARE_NOT_ESTIMATED: 'Стоимость проезда не рассчитана.',
  TRANSPORT_COST_NOT_INCLUDED: 'Стоимость проезда не включена в расходы маршрута.',
  UNVERIFIED_TRANSITION: 'Время перехода приблизительное. Заложите небольшой запас.',
  OPENING_HOURS_UNKNOWN: 'Уточните часы работы перед посещением.',
  ARCHETYPE_MATCH: 'Посещение подходит к теме маршрута.',
  INTEREST_MATCH: 'Посещение соответствует вашим интересам.',
  REST_BREAK: 'Небольшая пауза для отдыха.',
  NO_FEASIBLE_ROUTE: 'Не удалось подобрать маршрут с этими условиями. Попробуйте изменить время или ограничения.',
  OBLIGATION_SOLD_OUT: 'На обязательное посещение нет свободных мест.',
  OBLIGATION_CANCELLED: 'Обязательное посещение отменено.',
  UNKNOWN_PRICE: 'Стоимость нужно уточнить.',
  SEMANTIC_SEARCH_UNAVAILABLE: 'Пожелания в свободном тексте не удалось учесть. Остальные условия сохранены.',
};

export function userMessage(item) {
  if (messages[item?.code]) return messages[item.code];
  const message = typeof item === 'string' ? item : item?.message;
  return typeof message === 'string' && /[А-Яа-яЁё]/.test(message) ? message : 'Условия этого посещения нужно уточнить.';
}
