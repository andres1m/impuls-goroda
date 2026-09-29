import { cities, zonedDateTime } from './input.js';
import { interests, interestMask } from './taxonomy.js';

export function localDateTime(value, timezone) {
  if (!value || !timezone) return '';
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(value)).map(({ type, value: part }) => [type, part]));
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
}

export function scenarioForm(input) {
  const constraints = input.constraints || {};
  const mask = BigInt(constraints.interest_mask || '0x0');
  const minor = constraints.budget?.limit?.amount_minor;
  return {
    start: localDateTime(input.start_at, input.timezone), end: localDateTime(input.end_at, input.timezone),
    interests: interests.filter((_, index) => (mask & (1n << BigInt(index))) !== 0n).map(([code]) => code),
    modes: constraints.movement_modes || [], profile: constraints.load_profile || '',
    excluded: constraints.excluded_categories || [],
    budgetMode: constraints.budget?.mode || '',
    budget: minor && constraints.budget.limit.currency === 'RUB' ? `${BigInt(minor) / 100n}.${String(BigInt(minor) % 100n).padStart(2, '0')}` : '',
    pushkin: constraints.pushkin_card_only === true, wishes: constraints.semantic_query || '',
  };
}

export function confirmedScenarioInput(base, form, city, origin, destination) {
  if (!cities[city]) throw new Error('Не удалось определить поддерживаемый город для старта.');
  if (!origin) throw new Error('Выберите старт на карте.');
  const timezone = cities[city].timezone;
  const start = zonedDateTime(form.start, timezone);
  const end = zonedDateTime(form.end, timezone);
  if (end.timestamp <= start.timestamp) throw new Error('Завершение должно быть позже начала.');
  if (!form.modes.length) throw new Error('Выберите способ передвижения.');
  if (!form.profile || !form.budgetMode) throw new Error('Укажите темп и условия бюджета.');
  const old = base.constraints || {};
  let budget = { mode: form.budgetMode };
  if (form.budgetMode !== 'none') {
    if (old.budget?.limit?.currency && old.budget.limit.currency !== 'RUB') throw new Error('Бюджет сценария задан не в рублях. Эта форма пока поддерживает только рубли или расчёт без ограничения бюджета.');
    if (!/^(0|[1-9][0-9]*)([.,][0-9]{1,2})?$/.test(form.budget)) throw new Error('Укажите сумму бюджета в рублях.');
    const [whole, fraction = ''] = form.budget.replace(',', '.').split('.');
    const amount = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'));
    if (amount > 9223372036854775807n) throw new Error('Сумма бюджета слишком велика.');
    budget.limit = { amount_minor: amount.toString(), currency: 'RUB' };
  }
  const unknownBits = BigInt(old.interest_mask || '0x0') & ~((1n << BigInt(interests.length)) - 1n);
  const mask = unknownBits | BigInt(interestMask(form.interests));
  const constraints = {
    ...old, interest_mask: `0x${mask.toString(16).padStart(16, '0')}`,
    excluded_categories: [...form.excluded], movement_modes: [...form.modes], load_profile: form.profile, budget,
    benefit_programs: old.benefit_programs || [], audience_claims: old.audience_claims || [], obligations: old.obligations || [],
    soft_preferences: old.soft_preferences || [], accepted_unknowns: old.accepted_unknowns || [], pushkin_card_only: form.pushkin,
  };
  if (form.wishes.trim()) {
    if ([...form.wishes].length > 1000) throw new Error('Пожелания должны быть не длиннее 1000 символов.');
    constraints.semantic_query = form.wishes;
  } else delete constraints.semantic_query;
  const input = { ...base, city, timezone, origin, constraints,
    start_at: base.timezone === timezone && form.start === localDateTime(base.start_at, timezone) ? base.start_at : start.value,
    end_at: base.timezone === timezone && form.end === localDateTime(base.end_at, timezone) ? base.end_at : end.value,
  };
  if (destination) input.destination = destination; else delete input.destination;
  return input;
}

export function draftScenarioInput(base, form, city, origin, destination) {
  const input = { ...base, constraints: { ...(base.constraints || {}) } };
  const constraints = input.constraints;
  if (city) {
    if (!cities[city]) throw new Error('Город старта пока не поддерживается.');
    input.city = city; input.timezone = cities[city].timezone;
  } else { delete input.city; delete input.timezone; }
  for (const [field, value] of [['start_at', form.start], ['end_at', form.end]]) {
    if (!value) { delete input[field]; continue; }
    if (!input.timezone) throw new Error('Выберите старт перед сохранением времени.');
    input[field] = base.timezone === input.timezone && value === localDateTime(base[field], input.timezone)
      ? base[field] : zonedDateTime(value, input.timezone).value;
  }
  if (input.start_at && input.end_at && Date.parse(input.end_at) <= Date.parse(input.start_at)) throw new Error('Завершение должно быть позже начала.');
  if (origin) input.origin = { ...origin }; else delete input.origin;
  if (destination) input.destination = { ...destination }; else delete input.destination;
  const unknownBits = BigInt(base.constraints?.interest_mask || '0x0') & ~((1n << BigInt(interests.length)) - 1n);
  constraints.interest_mask = `0x${(unknownBits | BigInt(interestMask(form.interests))).toString(16).padStart(16, '0')}`;
  constraints.excluded_categories = [...form.excluded];
  if (form.modes.length) constraints.movement_modes = [...form.modes]; else delete constraints.movement_modes;
  if (form.profile) constraints.load_profile = form.profile; else delete constraints.load_profile;
  if (form.budgetMode) {
    constraints.budget = { mode: form.budgetMode };
    if (form.budgetMode !== 'none' && base.constraints?.budget?.limit?.currency && base.constraints.budget.limit.currency !== 'RUB') throw new Error('Форма бюджета поддерживает только рубли.');
    if (form.budgetMode !== 'none' && form.budget) {
      if (!/^(0|[1-9][0-9]*)([.,][0-9]{1,2})?$/.test(form.budget)) throw new Error('Укажите корректную сумму бюджета.');
      const [whole, fraction = ''] = form.budget.replace(',', '.').split('.');
      const amount = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'));
      if (amount > 9223372036854775807n) throw new Error('Сумма бюджета слишком велика.');
      constraints.budget.limit = { amount_minor: amount.toString(), currency: 'RUB' };
    }
  } else delete constraints.budget;
  constraints.pushkin_card_only = form.pushkin;
  if (form.wishes.trim()) {
    if ([...form.wishes].length > 1000) throw new Error('Пожелания должны быть не длиннее 1000 символов.');
    constraints.semantic_query = form.wishes;
  } else delete constraints.semantic_query;
  return input;
}

export async function resolveStartCity(point, apiKey, signal) {
  if (!apiKey) throw new Error('Для определения города нужен доступ к карте.');
  const url = new URL('https://catalog.api.2gis.com/3.0/items/geocode');
  url.search = new URLSearchParams({ lat: String(point.latitude), lon: String(point.longitude), fields: 'items.adm_div,items.address', key: apiKey });
  const response = await fetch(url, { signal });
  if (!response.ok) throw new Error('Не удалось определить город. Попробуйте выбрать старт ещё раз.');
  const body = await response.json();
  if (body.meta?.code !== 200 || !Array.isArray(body.result?.items)) throw new Error('Не удалось определить город.');
  const names = new Set(body.result.items.flatMap((item) => (item.adm_div || []).filter((division) => division.type === 'city').map((division) => division.name)));
  const found = Object.entries(cities).filter(([, value]) => names.has(value.name));
  if (found.length !== 1) throw new Error('Для выбранного места пока нет городского каталога.');
  return found[0][0];
}
