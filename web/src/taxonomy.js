export const interests = [
  ['contemporary_art', 'Современное искусство'],
  ['classical_art', 'Музеи и история'],
  ['science_tech', 'Наука и технологии'],
  ['street_workout', 'Уличный спорт'],
  ['running_park', 'Бег и парки'],
  ['eco_volunteer', 'Эковолонтёрство'],
  ['social_volunteer', 'Социальные проекты'],
  ['gastro_coffee', 'Кофейни и гастрономия'],
  ['performing_arts', 'Театр и сцена'],
  ['excursions', 'Экскурсии'],
  ['city_walk', 'Городские прогулки'],
  ['lectures_workshops', 'Лекции и мастер-классы'],
  ['cinema', 'Кино'],
];

export const categories = [
  ['culture', 'Культура'],
  ['sport', 'Спорт'],
  ['volunteer', 'Волонтёрство'],
  ['walk', 'Прогулки'],
  ['tourism', 'Туризм'],
  ['gastro', 'Гастрономия'],
];

export const movementModes = [
  ['walk', 'Пешком'],
  ['transit', 'Общественный транспорт'],
];

export function interestMask(selected) {
  let mask = 0n;
  const seen = new Set();
  for (const code of selected) {
    const bit = interests.findIndex(([value]) => value === code);
    if (bit < 0 || seen.has(code)) throw new Error('Проверьте выбранные интересы.');
    seen.add(code);
    mask |= 1n << BigInt(bit);
  }
  return `0x${mask.toString(16).padStart(16, '0')}`;
}

export function validCodes(selected, options) {
  if (!Array.isArray(selected)) return false;
  const allowed = new Set(options.map(([code]) => code));
  return selected.every((code) => allowed.has(code)) && new Set(selected).size === selected.length;
}
