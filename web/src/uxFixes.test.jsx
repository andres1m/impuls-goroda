import assert from 'node:assert/strict';
import fs from 'node:fs';
import { test } from 'node:test';
import { createElement } from 'react';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><html><body></body></html>');
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.MutationObserver = dom.window.MutationObserver;

const { cleanup, fireEvent, render, screen } = await import('@testing-library/react');
const { default: OwnerRouteScreen } = await import('./OwnerRouteScreen.jsx');
const { default: RouteLibrary } = await import('./RouteLibrary.jsx');
const { default: ScenarioCreate } = await import('./ScenarioCreate.jsx');
const { default: ScenarioEntry } = await import('./ScenarioEntry.jsx');
const { default: ScenarioVariants } = await import('./ScenarioVariants.jsx');

test('OwnerRouteScreen does not emit duplicate React key warnings for saved routes', () => {
  const errors = [];
  const originalError = console.error;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(JSON.stringify({ code: 'NOT_FOUND', retryable: false }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    });
  console.error = (...args) => {
    errors.push(args.map(String).join(' '));
  };
  try {
    const route = {
      route_id: '11111111-1111-4111-8111-111111111111',
      city: 'perm',
      lifecycle: 'saved',
      revision: '1',
      plan: {
        timezone: 'Asia/Yekaterinburg',
        start_at: '2026-09-30T07:00:00Z',
        end_at: '2026-09-30T11:00:00Z',
        result: 'READY',
        warnings: [],
        cost: { known_personal: { amount_minor: '0', currency: 'RUB' }, unknown_components: [] },
        steps: [
          {
            visit_id: '22222222-2222-4222-8222-222222222222',
            kind: 'visit',
            position: 1,
            visit_start_at: '2026-09-30T07:30:00Z',
            visit_end_at: '2026-09-30T08:30:00Z',
            catalog: { title: 'Музей PERMM', category: 'culture', availability: 'available', data_mode: 'prepared' },
            cost: { unknown_components: [] },
          },
        ],
        legs: [],
      },
    };
    render(
      createElement(OwnerRouteScreen, {
        route,
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
      }),
    );
    const keyWarnings = errors.filter((msg) => msg.includes('same key'));
    assert.deepEqual(keyWarnings, []);
  } finally {
    console.error = originalError;
    globalThis.fetch = prevFetch;
    cleanup();
  }
});

test('main.jsx configures MaxUI with light colorScheme to match light app surfaces', () => {
  const mainSource = fs.readFileSync(new URL('./main.jsx', import.meta.url), 'utf8');
  assert.match(mainSource, /<MaxUI[^>]*colorScheme=["']light["']/);
});

test('entry-state buttons use stretched MaxUI Button and uniform full-width CSS rules', () => {
  const appSource = fs.readFileSync(new URL('./App.jsx', import.meta.url), 'utf8');
  const devPreviewSource = fs.readFileSync(new URL('./DevPreview.jsx', import.meta.url), 'utf8');
  const css = fs.readFileSync(new URL('./styles.css', import.meta.url), 'utf8');

  assert.match(appSource, /<Button\s+stretched\s+onClick=\{retry\}>Повторить<\/Button>/);
  assert.match(devPreviewSource, /<Button\s+stretched\s+onClick=/);
  assert.match(css, /\.entry-state\s+button\s*\{[^}]*width:\s*100%/);
});

test('styles.css enforces light color-scheme, dark text on light cards, and >=44px summary touch targets', () => {
  const css = fs.readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
  assert.match(css, /color-scheme:\s*light/);
  assert.match(css, /\.MaxUI__g7Q\s*\{[^}]*background-color:\s*#f5f7f8/);
  assert.match(css, /\.scenario-card\s*\{[^}]*color:\s*#191c22/);
  assert.match(css, /\.route-notes\s+summary\s*\{[^}]*min-height:\s*44px/);
  assert.match(css, /\.scenario-advanced\s+summary\s*\{[^}]*min-height:\s*44px/);
  assert.match(css, /\.route-library\s*\{[^}]*max-width:\s*760px/);
});

test('RouteLibrary renders modern cards with normalized city, time window, duration, and status badges', async () => {
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        routes: [
          {
            route_id: '11111111-1111-4111-8111-111111111111',
            lifecycle: 'saved',
            revision: '3',
            result: 'READY',
            city: 'perm',
            timezone: 'Asia/Yekaterinburg',
            archetype_id: 'urban_avantgarde',
            start_at: '2026-09-30T07:00:00Z',
            end_at: '2026-09-30T13:00:00Z',
            updated_at: '2026-09-30T06:30:00Z',
          },
          {
            route_id: '22222222-2222-4222-8222-222222222222',
            lifecycle: 'saved',
            revision: '2',
            result: 'PARTIAL',
            city: 'perm',
            timezone: 'Asia/Yekaterinburg',
            archetype_id: 'history_heritage',
            start_at: '2026-10-01T08:00:00Z',
            end_at: '2026-10-01T15:00:00Z',
            updated_at: '2026-09-30T06:15:00Z',
          },
        ],
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    );

  try {
    render(
      createElement(RouteLibrary, {
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
        onBack: () => {},
        onCreate: () => {},
        onAuthRequired: () => {},
      }),
    );
    await screen.findByText('Современный город');
    const text = document.body.textContent;
    assert.match(text, /Пермь/);
    assert.match(text, /12:00\s*[–-]\s*18:00/);
    assert.match(text, /6\s*ч/);
    assert.match(text, /Готов к прогулке/);
    assert.match(text, /Есть ограничения/);
    const card = document.querySelector('.route-library-card[data-archetype="urban_avantgarde"]');
    assert.ok(card, 'expected route card to include data-archetype attribute');
  } finally {
    cleanup();
    globalThis.fetch = prevFetch;
  }
});

test('ScenarioCreate renders compact topbar and rich theme cards with subtitles', () => {
  try {
    render(
      createElement(ScenarioCreate, {
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
        onCreated: () => {},
        onLibrary: () => {},
        onAuthRequired: () => {},
      }),
    );
    const topbar = document.querySelector('.scenario-topbar');
    assert.ok(topbar, 'expected ScenarioCreate to render .scenario-topbar');
    const themeCard = document.querySelector('.scenario-theme-card[data-theme="vibe"]');
    assert.ok(themeCard, 'expected ScenarioCreate to render rich .scenario-theme-card with data-theme');
    assert.ok(themeCard.querySelector('.scenario-theme-desc'), 'expected theme card to include a subtitle');
  } finally {
    cleanup();
  }
});

test('ScenarioEntry and ScenarioVariants render compact topbar and archetype-accented variant cards', async () => {
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        route: {
          route_id: '11111111-1111-4111-8111-111111111111',
          city: 'perm',
          lifecycle: 'draft',
          revision: '1',
          issues: [],
          plan: {
            archetype_id: 'history_heritage',
            timezone: 'Asia/Yekaterinburg',
            start_at: '2026-09-30T07:00:00Z',
            end_at: '2026-09-30T11:00:00Z',
            result: 'READY',
            warnings: [],
            cost: {
              known_personal: { amount_minor: '50000', currency: 'RUB' },
              known_transport: { amount_minor: '0', currency: 'RUB' },
              unknown_components: [],
            },
            steps: [
              {
                visit_id: '22222222-2222-4222-8222-222222222222',
                kind: 'visit',
                position: 1,
                visit_start_at: '2026-09-30T07:30:00Z',
                visit_end_at: '2026-09-30T08:30:00Z',
                catalog: {
                  place_id: 'p1',
                  title: 'Краеведческий музей',
                  category: 'culture',
                  availability: 'available',
                  data_mode: 'prepared',
                },
                cost: { unknown_components: [] },
              },
            ],
            legs: [],
          },
        },
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    );

  try {
    render(
      createElement(ScenarioEntry, {
        scenario: {
          scenario_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
          version: '1',
          status: 'draft',
          source: 'custom',
          source_text: 'Хочу прогулку по центру',
          input: {
            city: 'perm',
            timezone: 'Asia/Yekaterinburg',
            start_at: '2026-09-30T07:00:00Z',
            end_at: '2026-09-30T11:00:00Z',
            origin: { latitude: 58.01, longitude: 56.25 },
            constraints: {
              movement_modes: ['walk'],
              load_profile: 'moderate',
              budget: { mode: 'none' },
              interests: ['culture'],
              excluded_categories: [],
            },
          },
        },
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
        mapApiKey: '',
        onLibrary: () => {},
      }),
    );
    assert.ok(document.querySelector('.scenario-topbar'), 'expected ScenarioEntry to render .scenario-topbar');
    cleanup();

    render(
      createElement(ScenarioVariants, {
        routeIDs: ['11111111-1111-4111-8111-111111111111'],
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
        disabled: false,
        loadingID: null,
        retrySelectionID: null,
        onSelect: () => {},
      }),
    );
    await screen.findByText('История и культура');
    const variantCard = document.querySelector('.variant-card[data-archetype="history_heritage"]');
    assert.ok(variantCard, 'expected VariantCard to include data-archetype="history_heritage"');
  } finally {
    cleanup();
    globalThis.fetch = prevFetch;
  }
});

test('movementModes includes car ("На автомобиле") for ScenarioEntry and scenarioExtraction', async () => {
  const { reviewExtraction } = await import('./scenarioExtraction.js');
  const proposal = reviewExtraction({
    constraints: {
      movement_modes: ['car', 'walk'],
    },
  });
  assert.deepEqual(proposal.formPatch.modes, ['car', 'walk']);
  assert.deepEqual(proposal.rows, [['Передвижение', 'На автомобиле, Пешком']]);
});

test('ScenarioEntry renders compact interest chips, 1-click presets, expand toggle, and keeps advanced section closed when lunch is enabled', () => {
  try {
    render(
      createElement(ScenarioEntry, {
        scenario: {
          scenario_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
          version: '1',
          status: 'draft',
          source: 'custom',
          source_text: 'Маршрут на выходной',
          input: {
            city: 'perm',
            timezone: 'Asia/Yekaterinburg',
            start_at: '2026-09-30T07:00:00Z',
            end_at: '2026-09-30T13:00:00Z',
            origin: { latitude: 58.01, longitude: 56.25 },
            constraints: {
              movement_modes: ['walk'],
              load_profile: 'moderate',
              budget: { mode: 'none' },
              interests: [],
              excluded_categories: [],
              lunch_window: {
                start_at: '2026-09-30T08:00:00Z',
                end_at: '2026-09-30T09:30:00Z',
                min_duration_seconds: 3600,
              },
            },
          },
        },
        apiBaseUrl: 'http://localhost:8080',
        accessToken: 'dev-token',
        mapApiKey: '',
        onLibrary: () => {},
      }),
    );

    const advanced = document.querySelector('details.scenario-advanced');
    assert.ok(advanced, 'expected details.scenario-advanced to exist');
    assert.equal(advanced.hasAttribute('open'), false, 'expected details.scenario-advanced not to be forced open by lunch_window');

    const expandBtn = screen.getByRole('button', { name: /\+\s*Ещё\s*7\s*тем/i });
    assert.ok(expandBtn, 'expected expand button for remaining 7 interests');
    assert.equal(screen.queryByLabelText('Кино'), null, 'expected rare interest "Кино" to be collapsed by default');

    fireEvent.click(expandBtn);
    assert.ok(screen.getByLabelText('Кино'), 'expected "Кино" to appear after expanding');

    const presetBtn = screen.getByRole('button', { name: /Прогулки и кофе/i });
    fireEvent.click(presetBtn);
    assert.equal(screen.getByLabelText('Городские прогулки').checked, true);
    assert.equal(screen.getByLabelText('Кофейни и гастрономия').checked, true);

    const resetBtn = screen.getByRole('button', { name: /Сбросить/i });
    fireEvent.click(resetBtn);
    assert.equal(screen.getByLabelText('Городские прогулки').checked, false);

    const css = fs.readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
    assert.match(css, /\.scenario-choices\s*\{[^}]*display:\s*flex;[^}]*flex-wrap:\s*wrap/);
    assert.match(css, /\.scenario-chips\s+input\[type=checkbox\]\s*\{[^}]*opacity:\s*0/);
  } finally {
    cleanup();
  }
});
