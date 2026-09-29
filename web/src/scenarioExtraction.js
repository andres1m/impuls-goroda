import { categories, interests, movementModes, validCodes } from './taxonomy.js';

const profiles = { relaxed: 'Спокойный', moderate: 'Умеренный', intense: 'Активный' };
const budgets = { none: 'Без ограничения', advisory: 'Ориентир', strict: 'Не превышать' };
const allowed = new Set(['interest_mask', 'excluded_categories', 'movement_modes', 'load_profile', 'budget', 'pushkin_card_only']);
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const names = (values, options) => values.map((code) => options.find(([value]) => value === code)[1]).join(', ');
function invalid() { throw new Error('Предложение содержит условия, которые эта форма не поддерживает. Укажите их вручную.'); }

export function reviewExtraction(input) {
  if (!object(input) || Object.keys(input).some((key) => key !== 'constraints') || !object(input.constraints)) invalid();
  const c = input.constraints;
  if (!Object.keys(c).length || Object.keys(c).some((key) => !allowed.has(key))) invalid();
  const rows = [], formPatch = {};
  if ('interest_mask' in c) {
    if (typeof c.interest_mask !== 'string' || !/^0x[0-9a-f]{16}$/i.test(c.interest_mask)) invalid();
    const mask = BigInt(c.interest_mask);
    if (mask >> BigInt(interests.length)) invalid();
    formPatch.interests = interests.filter((_, i) => mask & (1n << BigInt(i))).map(([code]) => code);
    rows.push(['Интересы', names(formPatch.interests, interests) || 'Не указаны']);
  }
  if ('excluded_categories' in c) {
    if (!validCodes(c.excluded_categories, categories)) invalid();
    formPatch.excluded = [...c.excluded_categories];
    rows.push(['Исключить', names(c.excluded_categories, categories) || 'Нет исключений']);
  }
  if ('movement_modes' in c) {
    if (!validCodes(c.movement_modes, movementModes) || !c.movement_modes.length) invalid();
    formPatch.modes = [...c.movement_modes];
    rows.push(['Передвижение', names(c.movement_modes, movementModes)]);
  }
  if ('load_profile' in c) {
    if (!Object.hasOwn(profiles, c.load_profile)) invalid();
    formPatch.profile = c.load_profile;
    rows.push(['Темп', profiles[c.load_profile]]);
  }
  if ('budget' in c) {
    const budget = c.budget;
    if (!object(budget) || Object.keys(budget).some((key) => !['mode', 'limit'].includes(key)) || !Object.hasOwn(budgets, budget.mode)) invalid();
    formPatch.budgetMode = budget.mode; formPatch.budget = '';
    if (budget.limit !== undefined) {
      const limit = budget.limit;
      if (budget.mode === 'none' || !object(limit) || Object.keys(limit).length !== 2 || limit.currency !== 'RUB' ||
          typeof limit.amount_minor !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(limit.amount_minor)) invalid();
      const amount = BigInt(limit.amount_minor);
      if (amount > 9223372036854775807n) invalid();
      formPatch.budget = `${amount / 100n}.${String(amount % 100n).padStart(2, '0')}`;
    }
    rows.push(['Бюджет', `${budgets[budget.mode]}${formPatch.budget ? ` · ${formPatch.budget} ₽` : budget.mode === 'none' ? '' : ' · сумму нужно уточнить'}`]);
  }
  if ('pushkin_card_only' in c) {
    if (typeof c.pushkin_card_only !== 'boolean') invalid();
    formPatch.pushkin = c.pushkin_card_only;
    rows.push(['Пушкинская карта', c.pushkin_card_only ? 'Только подходящие события' : 'Без ограничения']);
  }
  return { rows, formPatch };
}
