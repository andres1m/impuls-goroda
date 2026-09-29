import React, { useEffect, useState } from 'react';
import App from './App.jsx';
import PrototypeRouteScreen from './PrototypeRouteScreen.jsx';
import ScenarioEntry from './ScenarioEntry.jsx';
import OwnerRouteScreen from './OwnerRouteScreen.jsx';
import RouteLibrary from './RouteLibrary.jsx';
import SharedRouteScreen from './SharedRouteScreen.jsx';
import ScenarioCreate from './ScenarioCreate.jsx';
import Brand from './Brand.jsx';
import { Button } from '@maxhub/max-ui';

const ROUTE_1_ID = '11111111-1111-4111-8111-111111111111';
const ROUTE_2_ID = '22222222-2222-4222-8222-222222222222';
const ROUTE_3_ID = '33333333-3333-4333-8333-333333333333';
const SCENARIO_DRAFT_ID = '0192384a-9b1c-7f28-8000-000000000001';
const SCENARIO_DONE_ID = '0192384a-9b1c-7f28-8000-000000000002';

// 43-char base64url of 32 bytes (0x00..0x1f)
const VALID_SHARE_TOKEN = btoa(String.fromCharCode(...Array.from({ length: 32 }, (_, i) => i)))
  .replaceAll('+', '-')
  .replaceAll('/', '_')
  .replace(/=+$/, '');

const VISIT_1 = 'aaaaaaaa-1111-4111-8111-111111111111';
const VISIT_2 = 'bbbbbbbb-2222-4222-8222-222222222222';
const VISIT_3 = 'cccccccc-3333-4333-8333-333333333333';
const VISIT_LUNCH = 'dddddddd-4444-4444-8444-444444444444';

function makeMockRoute(id, archetype = 'urban_avantgarde', lifecycle = 'saved', withProposal = false) {
  const isHistory = archetype === 'history_heritage';
  const isAction = archetype === 'action_social';
  const steps = [
    {
      visit_id: VISIT_1,
      kind: 'visit',
      position: 1,
      arrival_at: '2026-09-30T07:00:00Z',
      visit_start_at: '2026-09-30T07:00:00Z',
      visit_end_at: '2026-09-30T08:15:00Z',
      departure_at: '2026-09-30T08:15:00Z',
      pinned: true,
      obligation: false,
      applied_constraints: [],
      participation: { status: 'not_required', evidence: 'catalog' },
      cost: {
        personal_amount: { amount_minor: '0', currency: 'RUB' },
        unknown_components: [],
      },
      catalog: {
        place_id: `place-1-${archetype}`,
        title: isHistory ? 'Пермская художественная галерея' : isAction ? 'Экстрим-парк и набережная' : 'Набережная Камы · Арт-объекты',
        category: isAction ? 'sport' : 'walk',
        availability: 'available',
        data_mode: 'prepared',
      },
    },
    {
      visit_id: VISIT_LUNCH,
      kind: 'free_time',
      position: 2,
      arrival_at: '2026-09-30T08:30:00Z',
      visit_start_at: '2026-09-30T08:30:00Z',
      visit_end_at: '2026-09-30T09:15:00Z',
      departure_at: '2026-09-30T09:15:00Z',
      applied_constraints: [{ code: 'LUNCH_WINDOW', message: 'Обеденная пауза по вашему расписанию' }],
    },
    {
      visit_id: VISIT_2,
      kind: 'visit',
      position: 3,
      arrival_at: '2026-09-30T09:35:00Z',
      visit_start_at: '2026-09-30T09:40:00Z',
      visit_end_at: '2026-09-30T11:00:00Z',
      departure_at: '2026-09-30T11:00:00Z',
      pinned: false,
      obligation: false,
      applied_constraints: [{ code: 'PUSHKIN_CARD', message: 'Доступна оплата по Пушкинской карте' }],
      participation: { status: 'action_required', evidence: 'catalog' },
      cost: {
        personal_amount: { amount_minor: '45000', currency: 'RUB' },
        unknown_components: [],
      },
      catalog: {
        place_id: `place-2-${archetype}`,
        event_id: `event-2-${archetype}`,
        session_id: `sess-2-${archetype}`,
        title: isHistory ? 'Краеведческий музей · Дом Мешкова' : isAction ? 'Лекторий на Заводе Шпагина' : 'Музей современного искусства PERMM',
        category: 'culture',
        availability: withProposal ? 'cancelled' : 'registration_required',
        data_mode: 'live',
        registration_details: 'Билет рекомендуется оформить заранее на сайте музея.',
        age_requirements: '12+',
      },
    },
    {
      visit_id: VISIT_3,
      kind: 'visit',
      position: 4,
      arrival_at: '2026-09-30T11:25:00Z',
      visit_start_at: '2026-09-30T11:30:00Z',
      visit_end_at: '2026-09-30T13:00:00Z',
      departure_at: '2026-09-30T13:00:00Z',
      pinned: false,
      obligation: false,
      applied_constraints: [],
      participation: { status: 'user_reported_confirmed', evidence: 'user' },
      cost: {
        personal_amount: { amount_minor: '0', currency: 'RUB' },
        unknown_components: [{ kind: 'cafe', message: 'Личные расходы по желанию' }],
      },
      catalog: {
        place_id: `place-3-${archetype}`,
        title: isHistory ? 'Пермский академический Театр-Театр' : isAction ? 'Городской сад им. Горького' : 'Центр городской культуры',
        category: 'culture',
        availability: 'available',
        data_mode: 'prepared',
      },
    },
  ];

  const legs = [
    {
      position: 1,
      from_kind: 'visit',
      from_visit_id: VISIT_1,
      to_kind: 'visit',
      to_visit_id: VISIT_2,
      mode: 'walk',
      verification: 'verified',
      distance_meters: 1450,
      departure_at: '2026-09-30T08:15:00Z',
      arrival_at: '2026-09-30T09:35:00Z',
      geometry: [
        { latitude: 58.0211, longitude: 56.2507 },
        { latitude: 58.0122, longitude: 56.2103 },
      ],
    },
    {
      position: 2,
      from_kind: 'visit',
      from_visit_id: VISIT_2,
      to_kind: 'visit',
      to_visit_id: VISIT_3,
      mode: isHistory ? 'transit' : 'walk',
      verification: isHistory ? 'estimated' : 'verified',
      distance_meters: 1900,
      departure_at: '2026-09-30T11:00:00Z',
      arrival_at: '2026-09-30T11:25:00Z',
      geometry: [
        { latitude: 58.0122, longitude: 56.2103 },
        { latitude: 58.0091, longitude: 56.2518 },
      ],
    },
  ];

  const plan = {
    city: 'perm',
    timezone: 'Asia/Yekaterinburg',
    start_at: '2026-09-30T07:00:00Z',
    end_at: '2026-09-30T13:00:00Z',
    result: withProposal ? 'PARTIAL' : 'READY',
    archetype_id: archetype,
    catalog_revision: '10',
    origin: { latitude: 58.0211, longitude: 56.2507 },
    destination: { latitude: 58.0091, longitude: 56.2518 },
    constraints: {
      pushkin_card_only: false,
    },
    cost: {
      known_personal: { amount_minor: isHistory ? '60000' : '45000', currency: 'RUB' },
      known_transport: { amount_minor: isHistory ? '4000' : '0', currency: 'RUB' },
      program_amount: { amount_minor: '45000', currency: 'RUB' },
      unknown_components: [{ kind: 'meal', message: 'Обед и кофе оплачиваются отдельно' }],
      budget_conclusion: 'satisfied',
    },
    warnings: withProposal
      ? [{ code: 'SESSION_CANCELLED', message: 'Один из сеансов отменён организатором. Проверьте предложенную замену.' }]
      : [],
    steps,
    legs,
  };

  const route = {
    route_id: id,
    city: 'perm',
    lifecycle,
    revision: '3',
    updated_at: '2026-09-30T06:30:00Z',
    plan,
    execution: [
      {
        visit_id: VISIT_1,
        status: 'completed',
        confirmation_kind: 'user_reported',
        actual_started_at: '2026-09-30T07:00:00Z',
        actual_ended_at: '2026-09-30T08:10:00Z',
      },
    ],
    participation: [
      { visit_id: VISIT_3, status: 'user_reported_confirmed', evidence: 'user' },
    ],
    issues: withProposal
      ? [
          {
            issue_id: 'eeeeeeee-5555-4555-8555-555555555555',
            type: 'cancelled',
            state: 'open',
            visit_id: VISIT_2,
            message: 'Сеанс 14:40 в музее PERMM отменён организатором.',
          },
        ]
      : [],
  };

  if (withProposal) {
    const candidateSteps = steps.map((s) =>
      s.visit_id === VISIT_2
        ? {
            ...s,
            catalog: {
              ...s.catalog,
              title: 'Арт-пространство «Завод Шпагина» (Замена)',
              availability: 'available',
            },
          }
        : s,
    );
    route.pending_proposal = {
      proposal_id: 'ffffffff-6666-4666-8666-666666666666',
      state: 'pending',
      reason: 'cancel',
      base_revision: '3',
      base_catalog_revision: '10',
      created_at: '2026-09-30T06:40:00Z',
      conflicts: [],
      changes: [
        {
          kind: 'replaced',
          scope: 'visit',
          before_visit_id: VISIT_2,
          after_visit_id: VISIT_2,
          message: 'Музей PERMM заменён на «Завод Шпагина» в том же временном окне.',
        },
        {
          kind: 'cost_changed',
          scope: 'route',
          message: 'Личные расходы уменьшились на 450 ₽.',
        },
      ],
      candidate: {
        ...plan,
        result: 'READY',
        steps: candidateSteps,
      },
    };
  }

  return route;
}

const MOCK_SCENARIO_DRAFT = {
  scenario_id: SCENARIO_DRAFT_ID,
  version: '1',
  status: 'draft',
  updated_at: '2026-09-30T06:00:00Z',
  source: 'custom',
  source_text: 'Хочу погулять в Перми с 12:00 до 18:00: набережная, современное искусство и уютный обед. Бюджет до 1500 рублей, пешком.',
  pending_extraction: {
    constraints: {
      interest_mask: '0x0000000000000005',
      excluded_categories: ['sport'],
      movement_modes: ['walk'],
      load_profile: 'moderate',
      budget: { mode: 'advisory', limit: { amount_minor: '150000', currency: 'RUB' } },
      pushkin_card_only: false,
    },
  },
  input: {
    city: 'perm',
    timezone: 'Asia/Yekaterinburg',
    start_at: '2026-09-30T07:00:00Z',
    end_at: '2026-09-30T13:00:00Z',
    origin: { latitude: 58.0105, longitude: 56.2502 },
    destination: { latitude: 58.0091, longitude: 56.2518 },
    constraints: {
      interest_mask: '0x0000000000000005',
      excluded_categories: [],
      movement_modes: ['walk'],
      load_profile: 'moderate',
      budget: { mode: 'advisory', limit: { amount_minor: '150000', currency: 'RUB' } },
      pushkin_card_only: false,
      lunch_window: {
        start_at: '2026-09-30T08:00:00Z',
        end_at: '2026-09-30T09:30:00Z',
        min_duration_seconds: 2700,
      },
      semantic_query: 'Хочется красивых видов на Каму и современную архитектуру',
    },
  },
};

const MOCK_SCENARIO_COMPLETED = {
  ...MOCK_SCENARIO_DRAFT,
  scenario_id: SCENARIO_DONE_ID,
  status: 'completed',
  pending_extraction: undefined,
  outcome: {
    status: 'READY',
    data_mode: 'prepared',
    route_ids: [ROUTE_1_ID, ROUTE_2_ID, ROUTE_3_ID],
    warnings: [
      { code: 'WEATHER_NOTE', message: 'Рекомендуем проверить расписание речных прогулок на набережной.' },
    ],
    conflicts: [],
  },
};

const SCREENS = [
  { id: 'prototype', label: '1. Демо (Прототип)' },
  { id: 'scenario-create', label: '2. Новый сценарий' },
  { id: 'scenario-draft', label: '3. Форма условий (ScenarioEntry)' },
  { id: 'scenario-variants', label: '4. Выбор из 3 вариантов' },
  { id: 'owner-saved', label: '5. Маршрут владельца' },
  { id: 'owner-proposal', label: '6. Отмена и перестроение' },
  { id: 'library', label: '7. Мои маршруты' },
  { id: 'shared', label: '8. Общий маршрут (Read-only)' },
  { id: 'entry-state', label: '9. Заглушка входа MAX' },
];

export default function DevPreview() {
  const [screen, setScreen] = useState(() => {
    const param = new URLSearchParams(window.location.search).get('screen');
    return SCREENS.some((s) => s.id === param) ? param : 'prototype';
  });
  const [collapsed, setCollapsed] = useState(false);
  const [notifEnabled, setNotifEnabled] = useState(true);
  const [notifVersion, setNotifVersion] = useState('1');
  const [mapApiKey, setMapApiKey] = useState('');

  useEffect(() => {
    fetch('/config.json', { cache: 'no-store' })
      .then((res) => (res.ok ? res.json() : null))
      .then((cfg) => {
        if (cfg && typeof cfg.twoGisApiKey === 'string') setMapApiKey(cfg.twoGisApiKey);
      })
      .catch(() => {});
  }, []);

  const stateRef = React.useRef({ screen, notifEnabled, notifVersion });
  stateRef.current = { screen, notifEnabled, notifVersion };

  if (typeof window !== 'undefined' && !window.__DEV_PREVIEW_FETCH_INSTALLED__) {
    window.__DEV_PREVIEW_FETCH_INSTALLED__ = true;
    const originalFetch = window.fetch.bind(window);

    if (!navigator.geolocation || typeof navigator.geolocation.getCurrentPosition === 'function') {
      const origGetPos = navigator.geolocation?.getCurrentPosition?.bind(navigator.geolocation);
      try {
        Object.defineProperty(navigator, 'geolocation', {
          configurable: true,
          value: {
            getCurrentPosition(success, error, options) {
              if (origGetPos) {
                origGetPos(
                  success,
                  () => success({ coords: { latitude: 58.0122, longitude: 56.235 } }),
                  { ...options, timeout: 1500 },
                );
              } else {
                success({ coords: { latitude: 58.0122, longitude: 56.235 } });
              }
            },
          },
        });
      } catch {
        // ignore if read-only
      }
    }

    window.fetch = async (input, init) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input?.url || '';
      const method = (init?.method || 'GET').toUpperCase();
      const current = window.__DEV_PREVIEW_STATE__ || {
        screen: 'prototype',
        notifEnabled: true,
        notifVersion: '1',
      };

      if (url.includes('/api/v1/health/ready')) {
        return new Response(
          JSON.stringify({
            status: 'available',
            checked_at: new Date().toISOString(),
            request_id: 'req-dev-health',
            capabilities: {
              read_saved_routes: { status: 'available' },
              mutate_routes: { status: 'available' },
              optimize: { status: 'available' },
              catalog_updates: { status: 'available' },
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      if (url.includes('/api/v1/routes?')) {
        const isDraft = url.includes('lifecycle=draft');
        const routes = [
          {
            route_id: ROUTE_1_ID,
            lifecycle: isDraft ? 'draft' : 'saved',
            revision: '3',
            result: 'READY',
            city: 'Пермь',
            timezone: 'Asia/Yekaterinburg',
            archetype_id: 'urban_avantgarde',
            start_at: '2026-09-30T07:00:00Z',
            end_at: '2026-09-30T13:00:00Z',
            updated_at: '2026-09-30T06:30:00Z',
          },
          {
            route_id: ROUTE_2_ID,
            lifecycle: isDraft ? 'draft' : 'saved',
            revision: '2',
            result: 'PARTIAL',
            city: 'Пермь',
            timezone: 'Asia/Yekaterinburg',
            archetype_id: 'history_heritage',
            start_at: '2026-10-01T08:00:00Z',
            end_at: '2026-10-01T15:00:00Z',
            updated_at: '2026-09-30T06:15:00Z',
          },
        ];
        return new Response(JSON.stringify({ routes }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }

      const notifMatch = /\/api\/v1\/routes\/([^/]+)\/notifications$/.exec(url);
      if (notifMatch) {
        const routeId = notifMatch[1].toLowerCase();
        if (method === 'POST') {
          const parsed = JSON.parse(init?.body || '{}');
          const nextVer = String(BigInt(parsed.expected_version || current.notifVersion) + 1n);
          current.setNotifEnabled?.(Boolean(parsed.enabled));
          current.setNotifVersion?.(nextVer);
          return new Response(
            JSON.stringify({
              request_id: 'req-dev-notif-post',
              route_id: routeId,
              revision: '3',
              preference: {
                enabled: Boolean(parsed.enabled),
                version: nextVer,
                platform_state: 'available',
              },
            }),
            { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
          );
        }
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-notif-get',
            route_id: routeId,
            revision: '3',
            preference: {
              enabled: current.notifEnabled,
              version: current.notifVersion,
              platform_state: 'available',
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
        );
      }

      const shareMatch = /\/api\/v1\/routes\/([^/]+)\/share$/.exec(url);
      if (shareMatch) {
        if (method === 'DELETE') {
          return new Response(null, { status: 204 });
        }
        const parsed = JSON.parse(init?.body || '{}');
        const token = parsed.share_token || VALID_SHARE_TOKEN;
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-share',
            status: 'READY',
            revision: '3',
            share_token: token,
            created_at: '2026-09-30T06:50:00Z',
            deep_link: `https://max.ru/ImpulsGorodaBot?startapp=${token}`,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
        );
      }

      const lunchSearchMatch = /\/api\/v1\/routes\/([^/]+)\/lunch\/search$/.exec(url);
      if (lunchSearchMatch) {
        const parsed = JSON.parse(init?.body || '{}');
        const nowIso = new Date().toISOString();
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-lunch',
            searched_at: nowIso,
            radius_meters: parsed.radius_meters || 500,
            candidates: [
              {
                provider: '2gis',
                external_id: '70000001012345678',
                title: 'Кофейня «Февраль»',
                address: 'ул. Сибирская, 8',
                position: { latitude: 58.0135, longitude: 56.238 },
                distance_meters: 180,
                observed_at: nowIso,
                price_status: 'unknown',
                hours_status: 'unknown',
                availability_status: 'unknown',
              },
              {
                provider: '2gis',
                external_id: '70000001087654321',
                title: 'Гастробистро «Урал»',
                address: 'ул. Монастырская, 14',
                position: { latitude: 58.0155, longitude: 56.241 },
                distance_meters: 340,
                observed_at: nowIso,
                price_status: 'unknown',
                hours_status: 'unknown',
                availability_status: 'unknown',
              },
            ],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      const lunchOrgMatch = /\/api\/v1\/routes\/([^/]+)\/lunch\/organizations\/([^/]+)$/.exec(url);
      if (lunchOrgMatch) {
        const orgId = decodeURIComponent(lunchOrgMatch[2]);
        const nowIso = new Date().toISOString();
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-lunch-org',
            organization: {
              provider: '2gis',
              external_id: orgId,
              title: orgId.endsWith('78') ? 'Кофейня «Февраль»' : 'Гастробистро «Урал»',
              address: orgId.endsWith('78') ? 'ул. Сибирская, 8 · 1 этаж' : 'ул. Монастырская, 14',
              position: { latitude: 58.0135, longitude: 56.238 },
              observed_at: nowIso,
              price_status: 'unknown',
              hours_status: 'unknown',
              availability_status: 'unknown',
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      const sharedRouteMatch = /\/api\/v1\/shared-routes\/([^/]+)$/.exec(url);
      if (sharedRouteMatch) {
        const base = makeMockRoute(ROUTE_1_ID, 'urban_avantgarde', 'saved', false);
        const sharedRoute = {
          revision: base.revision,
          city: base.plan.city,
          timezone: base.plan.timezone,
          start_at: base.plan.start_at,
          end_at: base.plan.end_at,
          result: base.plan.result,
          archetype_id: base.plan.archetype_id,
          cost: {
            known_personal: base.plan.cost.known_personal,
            known_transport: base.plan.cost.known_transport,
            program_amount: base.plan.cost.program_amount,
            unknown_components: base.plan.cost.unknown_components,
          },
          warnings: base.plan.warnings,
          issues: [],
          steps: base.plan.steps.map(({ pinned, obligation, participation, ...publicStep }) => publicStep),
          legs: base.plan.legs,
          updated_at: base.updated_at,
        };
        return new Response(
          JSON.stringify({ request_id: 'req-dev-shared', route: sharedRoute }),
          { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
        );
      }

      const routeMatch = /\/api\/v1\/routes\/([0-9a-f-]{36})$/i.exec(url);
      if (routeMatch && method === 'GET') {
        const id = routeMatch[1].toLowerCase();
        const archetype =
          id === ROUTE_2_ID ? 'history_heritage' : id === ROUTE_3_ID ? 'action_social' : 'urban_avantgarde';
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-route',
            route: makeMockRoute(id, archetype, 'saved', current.screen === 'owner-proposal'),
          }),
          { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
        );
      }

      if (url.includes('/api/v1/me/scenario') || /\/api\/v1\/scenarios\/[0-9a-f-]{36}$/i.test(url)) {
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-scenario-get',
            scenario: MOCK_SCENARIO_COMPLETED,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      if (url.includes('/api/v1/me/context')) {
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-ctx',
            confirmed_input: {},
            selected_route_id: ROUTE_1_ID,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      if (url.endsWith('/api/v1/scenarios') && method === 'POST') {
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-scenario-create',
            scenario: MOCK_SCENARIO_DRAFT,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      const draftMatch = /\/api\/v1\/scenarios\/([0-9a-f-]{36})\/draft$/i.exec(url);
      if (draftMatch && method === 'POST') {
        const parsed = JSON.parse(init?.body || '{}');
        const nextVer = String(BigInt(parsed.expected_version || '1') + 1n);
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-scenario-draft',
            scenario: {
              ...MOCK_SCENARIO_DRAFT,
              scenario_id: draftMatch[1].toLowerCase(),
              version: nextVer,
              status: 'draft',
              outcome: undefined,
              input: parsed.input || MOCK_SCENARIO_DRAFT.input,
            },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      const completeMatch = /\/api\/v1\/scenarios\/([0-9a-f-]{36})\/complete$/i.exec(url);
      if (completeMatch && method === 'POST') {
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-scenario-complete',
            result: 'READY',
            data_mode: 'prepared',
            route_ids: [ROUTE_1_ID, ROUTE_2_ID, ROUTE_3_ID],
            warnings: [],
            conflicts: [],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }

      if (url.includes('/select')) {
        return new Response(
          JSON.stringify({
            request_id: 'req-dev-select',
            status: 'SELECTED',
            route_id: ROUTE_1_ID,
            revision: '3',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json', ETag: '"3"' } },
        );
      }

      return originalFetch(input, init);
    };
  }

  if (typeof window !== 'undefined') {
    window.__DEV_PREVIEW_STATE__ = {
      screen,
      notifEnabled,
      notifVersion,
      setNotifEnabled,
      setNotifVersion,
    };
  }

  function selectScreen(id) {
    setScreen(id);
    const url = new URL(window.location.href);
    url.searchParams.set('screen', id);
    window.history.replaceState({}, '', url);
  }

  return (
    <>
      <div
        style={{
          position: 'fixed',
          top: 10,
          right: 10,
          zIndex: 9999,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-end',
          gap: 6,
          fontFamily: 'system-ui, -apple-system, sans-serif',
        }}
      >
        {collapsed ? (
          <button
            type="button"
            onClick={() => setCollapsed(false)}
            style={{
              padding: '6px 12px',
              borderRadius: 999,
              border: '1px solid #cbd5e1',
              background: '#0f172aee',
              color: '#f8fafc',
              fontSize: 12,
              fontWeight: 600,
              boxShadow: '0 4px 12px rgba(0,0,0,0.18)',
              cursor: 'pointer',
            }}
          >
            🛠 Экраны ({SCREENS.find((s) => s.id === screen)?.label.split('.')[0]})
          </button>
        ) : (
          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              gap: 6,
              maxWidth: 'calc(100vw - 20px)',
              padding: '8px 10px',
              borderRadius: 14,
              border: '1px solid #334155',
              background: '#0f172af2',
              color: '#f8fafc',
              boxShadow: '0 8px 24px rgba(0,0,0,0.22)',
              backdropFilter: 'blur(8px)',
            }}
          >
            <span style={{ fontSize: 11, fontWeight: 700, color: '#94a3b8', padding: '0 4px' }}>DEV UX/UI:</span>
            {SCREENS.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => selectScreen(item.id)}
                style={{
                  padding: '5px 10px',
                  borderRadius: 8,
                  border: screen === item.id ? '1px solid #38bdf8' : '1px solid #334155',
                  background: screen === item.id ? '#0284c7' : '#1e293b',
                  color: '#fff',
                  fontSize: 11,
                  fontWeight: screen === item.id ? 700 : 500,
                  cursor: 'pointer',
                }}
              >
                {item.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => setCollapsed(true)}
              title="Свернуть панель"
              style={{
                padding: '5px 8px',
                borderRadius: 8,
                border: '1px solid #475569',
                background: 'transparent',
                color: '#cbd5e1',
                fontSize: 11,
                cursor: 'pointer',
              }}
            >
              Свернуть ✕
            </button>
          </div>
        )}
      </div>

      {screen === 'prototype' && <PrototypeRouteScreen mapApiKey={mapApiKey} />}
      {screen === 'scenario-create' && (
        <ScenarioCreate
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          onCreated={() => selectScreen('scenario-draft')}
          onLibrary={() => selectScreen('library')}
          onAuthRequired={() => selectScreen('entry-state')}
        />
      )}
      {screen === 'scenario-draft' && (
        <ScenarioEntry
          key="draft"
          scenario={MOCK_SCENARIO_DRAFT}
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          mapApiKey={mapApiKey}
          onLibrary={() => selectScreen('library')}
        />
      )}
      {screen === 'scenario-variants' && (
        <ScenarioEntry
          key="completed"
          scenario={MOCK_SCENARIO_COMPLETED}
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          mapApiKey={mapApiKey}
          onLibrary={() => selectScreen('library')}
        />
      )}
      {screen === 'owner-saved' && (
        <OwnerRouteScreen
          key="owner-saved"
          route={makeMockRoute(ROUTE_1_ID, 'urban_avantgarde', 'saved', false)}
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          mapApiKey={mapApiKey}
          backLabel="Мои маршруты"
          onBack={() => selectScreen('library')}
        />
      )}
      {screen === 'owner-proposal' && (
        <OwnerRouteScreen
          key="owner-proposal"
          route={makeMockRoute(ROUTE_1_ID, 'urban_avantgarde', 'saved', true)}
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          mapApiKey={mapApiKey}
          backLabel="К вариантам"
          onBack={() => selectScreen('scenario-variants')}
        />
      )}
      {screen === 'library' && (
        <RouteLibrary
          apiBaseUrl="http://localhost:8080"
          accessToken="dev-token"
          mapApiKey={mapApiKey}
          onCreate={() => selectScreen('scenario-create')}
          onAuthRequired={() => selectScreen('entry-state')}
          onBack={() => selectScreen('scenario-variants')}
        />
      )}
      {screen === 'shared' && (
        <SharedRouteScreen
          apiBaseUrl="http://localhost:8080"
          token={VALID_SHARE_TOKEN}
          mapApiKey={mapApiKey}
        />
      )}
      {screen === 'entry-state' && (
        <main className="entry-page">
          <header><Brand className="entry-brand" /></header>
          <section className="entry-state" aria-live="polite">
            <span className="entry-rule" aria-hidden="true" />
            <h1>Откройте приложение в MAX</h1>
            <p>Для просмотра личного маршрута нужен вход через MAX.</p>
            <Button stretched onClick={() => selectScreen('owner-saved')}>Повторить</Button>
            <button className="scenario-option" onClick={() => selectScreen('library')}>Мои маршруты</button>
          </section>
        </main>
      )}
    </>
  );
}
