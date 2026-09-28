export const prototypeScenarios = {
  vibe: { name: 'Вайб', variant: 'urban' },
  mood: { name: 'Настроение', variant: 'history' },
  culture: { name: 'Культура', variant: 'history' },
  energy: { name: 'Энергия', variant: 'action' },
  balance: { name: 'Баланс', variant: 'urban' },
  benefit: { name: 'Польза', variant: 'action' },
  custom: { name: 'Свой сценарий', variant: 'urban' },
};

export const demoCities = {
  perm: { name: 'Пермь', timezone: 'Asia/Yekaterinburg' },
  moscow: { name: 'Москва', timezone: 'Europe/Moscow' },
};

const demoRoutePoints = {
  perm: {
    'Набережная Камы': [58.0211, 56.2507],
    'Музей современного искусства PERMM': [58.012205, 56.210291],
    'Центр городской культуры': [58.009064, 56.251838],
    'Завод Шпагина': [58.018712, 56.252701],
  },
};

const routePatterns = [
  {
    id: 'urban', title: 'Современный город', focus: 'Арт, городская среда и новые места',
    why: 'Для интереса к современной культуре и прогулке без плотного графика.',
    color: '#155e58', times: [['12:00', '13:00'], ['13:25', '14:40'], ['15:05', '16:20']], legs: [25, 25],
  },
  {
    id: 'history', title: 'История и культура', focus: 'Архитектура, выставка и сцена',
    why: 'Больше культурных остановок и времени на осмотр.',
    color: '#755335', times: [['12:00', '13:10'], ['13:30', '14:45'], ['15:10', '16:40']], legs: [20, 25],
  },
  {
    id: 'action', title: 'Движение и польза', focus: 'Активность, прогулка и новое знание',
    why: 'Динамичный день. Условия участия в активностях нужно проверить.',
    color: '#355d75', times: [['12:00', '12:55'], ['13:20', '14:40'], ['15:10', '16:25']], legs: [25, 30],
  },
];

const places = {
  perm: {
    urban: [
      ['Набережная Камы', 'Прогулка', 'Идея для начала дня у воды.', 'unknown', null, 18, 72],
      ['Музей современного искусства PERMM', 'Культура', 'Выставку, часы работы и билеты нужно уточнить.', 'unknown', 'Модельная цена: 450 ₽', 45, 42],
      ['Центр городской культуры', 'Культура', 'Идея для завершающей остановки.', 'registration_required', null, 75, 59],
    ],
    history: [
      ['Зелёная линия', 'Прогулка', 'Пеший маршрут по истории города.', 'unknown', null, 17, 73],
      ['Пермская художественная галерея', 'Культура', 'Экспозицию и правила посещения нужно проверить.', 'unknown', 'Модельная цена: 500 ₽', 51, 43],
      ['Пермский Театр-Театр', 'Культура', 'Сеанс, доступность и стоимость неизвестны.', 'registration_required', null, 77, 57],
    ],
    action: [
      ['Экстрим-парк', 'Спорт', 'Активность выбирается с учётом самочувствия.', 'unknown', null, 16, 67],
      ['Прогулка по городу', 'Прогулка', 'Промежуточная активная остановка.', 'unknown', null, 43, 48],
      ['Открытый лекторий', 'Культура', 'Тема и расписание — демонстрационные.', 'unknown', null, 78, 28],
    ],
  },
  moscow: {
    urban: [
      ['Лужники', 'Спорт', 'Идея для прогулки и активности.', 'unknown', null, 18, 70],
      ['Дом культуры «ГЭС-2»', 'Культура', 'Программу и правила входа нужно проверить.', 'unknown', null, 47, 45],
      ['Центр современного искусства «Винзавод»', 'Культура', 'Выставка в этом примере не подтверждена.', 'unknown', 'Модельная цена: 500 ₽', 79, 58],
    ],
    history: [
      ['Бульварное кольцо', 'Прогулка', 'Идея для архитектурной прогулки.', 'unknown', null, 16, 70],
      ['Новая Третьяковка', 'Культура', 'Билеты и программу нужно проверить.', 'unknown', 'Модельная цена: 600 ₽', 47, 46],
      ['Концертный зал им. Чайковского', 'Культура', 'Сеанс и доступность в демо условные.', 'registration_required', null, 78, 30],
    ],
    action: [
      ['Парк Горького', 'Спорт', 'Начните с комфортной активности.', 'unknown', null, 19, 70],
      ['Воробьёвы горы', 'Прогулка', 'Активный участок прогулки.', 'unknown', null, 46, 42],
      ['Музей криптографии', 'Культура', 'Афишу и билеты нужно проверить.', 'unknown', null, 76, 59],
    ],
  },
};

const rebuildAlternatives = {
  perm: {
    urban: [
      [['Завод Шпагина', 'Культура'], ['Музей современного искусства PERMM', 'Культура']],
      [['Центр городской культуры', 'Культура'], ['Завод Шпагина', 'Культура']],
    ],
    history: [
      [['Дом Мешкова', 'Культура'], ['Пермская художественная галерея', 'Культура']],
      [['Пермский краеведческий музей', 'Культура'], ['Дом Мешкова', 'Культура']],
    ],
    action: [
      [['Набережная Камы', 'Прогулка'], ['Центр городской культуры', 'Культура']],
      [['Прогулка по городу', 'Прогулка'], ['Музей современного искусства PERMM', 'Культура']],
    ],
  },
  moscow: {
    urban: [
      [['Московский музей современного искусства', 'Культура'], ['Дом культуры «ГЭС-2»', 'Культура']],
      [['Парк Зарядье', 'Прогулка'], ['Московский музей современного искусства', 'Культура']],
    ],
    history: [
      [['Музей Москвы', 'Культура'], ['Концертный зал им. Чайковского', 'Культура']],
      [['Государственный исторический музей', 'Культура'], ['Музей Москвы', 'Культура']],
    ],
    action: [
      [['Нескучный сад', 'Прогулка'], ['Музей криптографии', 'Культура']],
      [['Воробьёвы горы', 'Прогулка'], ['Дом культуры «ГЭС-2»', 'Культура']],
    ],
  },
};

export function selectedPrototypeScenario(webApp, locationSearch) {
  const startParam = webApp?.initDataUnsafe?.start_param;
  const queryParam = new URLSearchParams(locationSearch).get('demo');
  const candidate = typeof startParam === 'string' && startParam.startsWith('demo_shared_')
    ? startParam.slice(12)
    : typeof startParam === 'string' && startParam.startsWith('demo_')
      ? startParam.slice(5) : queryParam;
  return Object.hasOwn(prototypeScenarios, candidate) ? candidate : 'vibe';
}

export function demoVariants(city, volunteerInterest = false) {
  const cityPlaces = places[city] || places.perm;
  return routePatterns.map((pattern) => ({
    ...pattern,
    stops: cityPlaces[pattern.id].map((original, index) => {
      const place = pattern.id === 'action' && index === 1 && volunteerInterest
        ? ['Идея эко-акции', 'Волонтёрство', 'Смена не подтверждена; требования и регистрация неизвестны.', 'registration_required', null, original[5], original[6]]
        : original;
      return ({
      id: `${pattern.id}-${index}`,
      title: place[0], category: place[1], description: place[2],
      availability: place[3], price: place[4], x: place[5], y: place[6],
      routePoint: demoRoutePoints[city]?.[place[0]] || null,
      pushkinExample: (city === 'perm' && pattern.id === 'urban' && index === 1)
        || (city === 'moscow' && pattern.id === 'history' && index === 1),
      start: pattern.times[index][0], end: pattern.times[index][1],
      dataMode: 'synthetic', source: 'Демонстрационный набор',
      });
    }),
  }));
}

export function demoRebuiltVariant(base, city, option) {
  if (!option) return base;
  const replacements = rebuildAlternatives[city]?.[base.id]?.[option - 1];
  if (!replacements) return base;
  return {
    ...base,
    stops: base.stops.map((stop, index) => index === 0 ? stop : {
      ...stop,
      id: `${base.id}-alternative-${option}-${index}`,
      title: replacements[index - 1][0],
      category: replacements[index - 1][1],
      routePoint: demoRoutePoints[city]?.[replacements[index - 1][0]] || null,
      description: 'Альтернатива из синтетического набора; событие, часы и доступность не проверены.',
      availability: 'unknown',
      price: null,
      pushkinExample: false,
    }),
  };
}

export function shiftClock(value, minutes) {
  const [hours, rest] = value.split(':').map(Number);
  const total = hours * 60 + rest + minutes;
  return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
}

export function prototypePushkinEligible(variant) {
  return variant.stops.every((stop) => stop.category !== 'Культура' || !stop.price || stop.pushkinExample === true);
}
