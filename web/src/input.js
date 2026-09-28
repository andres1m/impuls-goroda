import { categories, interestMask, movementModes, validCodes } from './taxonomy.js';

export const cities = {
  moscow: { name: 'Москва', timezone: 'Europe/Moscow' },
  perm: { name: 'Пермь', timezone: 'Asia/Yekaterinburg' },
};

export class PlanInputError extends Error {
  constructor(field, message) {
    super(message);
    this.name = 'PlanInputError';
    this.field = field;
  }
}

const localTimePattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

function localParts(timestamp, timezone) {
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  return Object.fromEntries(
    formatter.formatToParts(new Date(timestamp))
      .filter((part) => part.type !== 'literal')
      .map((part) => [part.type, Number(part.value)]),
  );
}

function offsetAt(timestamp, timezone) {
  const parts = localParts(timestamp, timezone);
  const localAsUTC = Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    parts.second,
  );
  return (localAsUTC - timestamp) / 60000;
}

export function zonedDateTime(localTime, timezone) {
  const match = localTimePattern.exec(localTime);
  if (!match) throw new Error('Укажите дату и время.');

  const [, year, month, day, hour, minute] = match;
  const localAsUTC = Date.UTC(+year, +month - 1, +day, +hour, +minute);
  if (!Number.isFinite(localAsUTC)) throw new Error('Укажите корректную дату и время.');

  let timestamp = localAsUTC - offsetAt(localAsUTC, timezone) * 60000;
  timestamp = localAsUTC - offsetAt(timestamp, timezone) * 60000;
  const parts = localParts(timestamp, timezone);
  if (
    parts.year !== +year || parts.month !== +month || parts.day !== +day ||
    parts.hour !== +hour || parts.minute !== +minute
  ) {
    throw new Error('Это местное время недоступно в выбранном городе.');
  }

  const offsetMinutes = offsetAt(timestamp, timezone);
  const sign = offsetMinutes >= 0 ? '+' : '-';
  const absolute = Math.abs(offsetMinutes);
  const offset = `${sign}${String(Math.floor(absolute / 60)).padStart(2, '0')}:${String(absolute % 60).padStart(2, '0')}`;
  return { timestamp, value: `${localTime}:00${offset}` };
}

export function validateStartInput(form) {
  const city = cities[form.city];
  if (!city) throw new PlanInputError('city', 'Выберите город.');

  let start;
  let end;
  try {
    start = zonedDateTime(form.startAt, city.timezone);
  } catch (error) {
    throw new PlanInputError('startAt', error.message);
  }
  try {
    end = zonedDateTime(form.endAt, city.timezone);
  } catch (error) {
    throw new PlanInputError('endAt', error.message);
  }
  if (end.timestamp <= start.timestamp) {
    throw new PlanInputError('endAt', 'Конец прогулки должен быть позже начала.');
  }

  if (form.latitude === '') {
    throw new PlanInputError('latitude', 'Укажите координаты точки старта.');
  }
  if (form.longitude === '') {
    throw new PlanInputError('longitude', 'Укажите координаты точки старта.');
  }
  const latitude = Number(form.latitude);
  const longitude = Number(form.longitude);
  if (!Number.isFinite(latitude) || latitude < -90 || latitude > 90) {
    throw new PlanInputError('latitude', 'Проверьте широту точки старта.');
  }
  if (!Number.isFinite(longitude) || longitude < -180 || longitude > 180) {
    throw new PlanInputError('longitude', 'Проверьте долготу точки старта.');
  }

  return {
    city: form.city,
    timezone: city.timezone,
    startAt: start.value,
    endAt: end.value,
    origin: { latitude, longitude },
  };
}

export function validatePreferencesInput(form) {
  if (!validCodes(form.movementModes, movementModes) || form.movementModes.length === 0) {
    throw new PlanInputError('movementModes', 'Выберите хотя бы один способ передвижения.');
  }
  if (!validCodes(form.excludedCategories, categories)) {
    throw new PlanInputError('excludedCategories', 'Проверьте исключённые категории.');
  }
  let mask;
  try {
    mask = interestMask(form.interests);
  } catch (error) {
    throw new PlanInputError('interests', error.message);
  }

  return {
    interestMask: mask,
    movementModes: [...form.movementModes],
    excludedCategories: [...form.excludedCategories],
  };
}

export function validatePlanInput(form) {
  return { ...validateStartInput(form), ...validatePreferencesInput(form) };
}
