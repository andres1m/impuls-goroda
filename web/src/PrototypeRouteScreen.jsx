import React, { useEffect, useMemo, useRef, useState } from 'react';
import { demoCities, demoRebuiltVariant, demoVariants, prototypePushkinEligible, prototypeScenarios, selectedPrototypeScenario, shiftClock } from './prototype.js';
import TwoGisRouteMap from './TwoGisRouteMap.jsx';
import Brand from './Brand.jsx';

const storageKey = 'impuls-route-preview';

function savedPreview() {
  try {
    const value = JSON.parse(window.localStorage.getItem(storageKey) || '{}');
    return value && typeof value === 'object' ? value : {};
  } catch { return {}; }
}

function launchIsShared() {
  const payload = window.WebApp?.initDataUnsafe?.start_param;
  return (typeof payload === 'string' && payload.startsWith('demo_shared_')) || new URLSearchParams(window.location.search).get('shared') === '1';
}

function nearbyDemoCity(latitude, longitude) {
  const centers = { perm: [58.0105, 56.2502], moscow: [55.7558, 37.6173] };
  const radians = (degrees) => degrees * Math.PI / 180;
  const distance = ([cityLatitude, cityLongitude]) => {
    const latitudeDelta = radians(cityLatitude - latitude);
    const longitudeDelta = radians(cityLongitude - longitude);
    const arc = Math.sin(latitudeDelta / 2) ** 2
      + Math.cos(radians(latitude)) * Math.cos(radians(cityLatitude)) * Math.sin(longitudeDelta / 2) ** 2;
    return 12742000 * Math.asin(Math.sqrt(arc));
  };
  const nearest = Object.entries(centers).map(([id, center]) => [id, distance(center)]).sort((a, b) => a[1] - b[1])[0];
  return nearest[1] <= 60000 ? nearest[0] : null;
}

function Sheet({ title, onClose, children }) {
  const sheetRef = useRef(null);
  useEffect(() => {
    const previous = document.activeElement;
    sheetRef.current?.focus();
    return () => previous?.focus?.();
  }, []);
  return (
    <div className="workspace-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="workspace-sheet" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} ref={sheetRef}>
        <div className="workspace-sheet-head"><h2>{title}</h2><button type="button" onClick={onClose} aria-label="Закрыть">×</button></div>
        <div className="workspace-sheet-body">{children}</div>
      </section>
    </div>
  );
}

export default function PrototypeRouteScreen({ mapApiKey = '' }) {
  const scenarioKey = selectedPrototypeScenario(window.WebApp, window.location.search);
  const initial = savedPreview();
  const initialVariant = prototypeScenarios[scenarioKey].variant;
  const [city, setCity] = useState(demoCities[initial.city] ? initial.city : 'perm');
  const [variantID, setVariantID] = useState(initial.scenario === scenarioKey && ['urban', 'history', 'action'].includes(initial.variantID) ? initial.variantID : initialVariant);
  const [routeOption, setRouteOption] = useState(initial.scenario === scenarioKey && [0, 1, 2].includes(initial.routeOption) ? initial.routeOption : 0);
  const [selectedID, setSelectedID] = useState(null);
  const [saved, setSaved] = useState(Boolean(initial.scenario === scenarioKey && initial.saved));
  const [pinned, setPinned] = useState(initial.scenario === scenarioKey && Array.isArray(initial.pinned) ? initial.pinned : []);
  const [completed, setCompleted] = useState(initial.scenario === scenarioKey && Array.isArray(initial.completed) ? initial.completed : []);
  const [reported, setReported] = useState(initial.scenario === scenarioKey && Array.isArray(initial.reported) ? initial.reported : []);
  const [removed, setRemoved] = useState(initial.scenario === scenarioKey && Array.isArray(initial.removed) ? initial.removed : []);
  const [pauses, setPauses] = useState(initial.scenario === scenarioKey && Array.isArray(initial.pauses) ? initial.pauses : []);
  const [shift, setShift] = useState(initial.scenario === scenarioKey && Number.isInteger(initial.shift) ? initial.shift : 0);
  const [revision, setRevision] = useState(initial.scenario === scenarioKey && Number.isInteger(initial.revision) ? initial.revision : 1);
  const [movement, setMovement] = useState('walk');
  const [budget, setBudget] = useState('');
  const [volunteerInterest, setVolunteerInterest] = useState(false);
  const [startPlace, setStartPlace] = useState(initial.startPlace || '');
  const [finishPlace, setFinishPlace] = useState(initial.finishPlace || '');
  const [startPoint, setStartPoint] = useState(Array.isArray(initial.startPoint) && initial.startPoint.length === 2 ? initial.startPoint : null);
  const [finishPoint, setFinishPoint] = useState(Array.isArray(initial.finishPoint) && initial.finishPoint.length === 2 ? initial.finishPoint : null);
  const [draftStart, setDraftStart] = useState(initial.startPlace || '');
  const [draftFinish, setDraftFinish] = useState(initial.finishPlace || '');
  const [draftStartPoint, setDraftStartPoint] = useState(Array.isArray(initial.startPoint) && initial.startPoint.length === 2 ? initial.startPoint : null);
  const [draftFinishPoint, setDraftFinishPoint] = useState(Array.isArray(initial.finishPoint) && initial.finishPoint.length === 2 ? initial.finishPoint : null);
  const [pushkinMode, setPushkinMode] = useState(initial.pushkinMode || 'none');
  const [lunches, setLunches] = useState(() => initial.scenario === scenarioKey && Array.isArray(initial.lunches) ? initial.lunches : initial.lunchAfterIndex === null ? [] : [{ id: 'initial-lunch', afterIndex: initial.lunchAfterIndex || 0, ...(initial.lunch || {}) }]);
  const [editingLunchID, setEditingLunchID] = useState(null);
  const lunch = lunches.find((item) => item.id === editingLunchID);
  const [draftLunchAfterIndex, setDraftLunchAfterIndex] = useState(0);
  const [lunchNavigation, setLunchNavigation] = useState(null);
  const [lunchRadius, setLunchRadius] = useState(500);
  const [lunchDuration, setLunchDuration] = useState(45);
  const [cafes, setCafes] = useState([]);
  const [cafeError, setCafeError] = useState('');
  const [cafeStatus, setCafeStatus] = useState('idle');
  const [lunchPoint, setLunchPoint] = useState(null);
  const [cancelledStop, setCancelledStop] = useState(false);
  const [panel, setPanel] = useState(null);
  const [proposal, setProposal] = useState(null);
  const [delayMode, setDelayMode] = useState('already_delayed');
  const [delayMinutes, setDelayMinutes] = useState(30);
  const [removalMode, setRemovalMode] = useState('pause');
  const [readOnly, setReadOnly] = useState(launchIsShared);
  const [notice, setNotice] = useState('');
  const [entryPoint, setEntryPoint] = useState('default');
  const [mapPickMode, setMapPickMode] = useState(null);
  const mapSection = useRef(null);
  const lunchLocationRequest = useRef(0);

  const variants = useMemo(() => demoVariants(city, volunteerInterest), [city, volunteerInterest]);
  const baseVariant = variants.find((item) => item.id === variantID) || variants[0];
  const variant = demoRebuiltVariant(baseVariant, city, routeOption);
  const availableLunchStops = variant.stops.filter((stop) => !removed.includes(stop.id));
  const routeLunches = lunches.map((item) => ({ ...item, afterStopID: availableLunchStops[Math.min(item.afterIndex, availableLunchStops.length - 1)]?.id }));
  const lunchOffsetBefore = (index) => routeLunches.reduce((sum, item) => {
    const after = variant.stops.findIndex((stop) => stop.id === item.afterStopID);
    return sum + (after >= 0 && index > after ? item.offset || 0 : 0);
  }, 0);
  const stops = variant.stops.map((stop, index) => ({
    ...stop, start: shiftClock(stop.start, shift + lunchOffsetBefore(index)), end: shiftClock(stop.end, shift + lunchOffsetBefore(index)),
    pinned: pinned.includes(stop.id), completed: completed.includes(stop.id),
    reported: reported.includes(stop.id), removed: removed.includes(stop.id), pause: pauses.includes(stop.id),
  }));
  const selected = stops.find((stop) => stop.id === selectedID) || stops.find((stop) => !stop.removed) || stops[0];
  const visible = stops.filter((stop) => !stop.removed);
  const finishTime = stops.at(-1).end;
  const strictPushkinConflict = pushkinMode === 'only' && !prototypePushkinEligible(variant);
  const conditionalPushkin = variant.stops.reduce((total, stop) => total + (stop.pushkinExample && stop.price ? Number(stop.price.match(/\d+/)?.[0] || 0) : 0), 0);

  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, JSON.stringify({ scenario: scenarioKey, city, variantID, routeOption, saved, pinned, completed, reported, removed, pauses, shift, revision, startPlace, finishPlace, startPoint, finishPoint, pushkinMode, lunches }));
    } catch { /* The preview remains available without browser storage. */ }
  }, [scenarioKey, city, variantID, routeOption, saved, pinned, completed, reported, removed, pauses, shift, revision, startPlace, finishPlace, startPoint, finishPoint, pushkinMode, lunches]);

  useEffect(() => {
    const bridge = window.WebApp;
    if (!bridge?.getLaunchContext) return;
    let active = true;
    Promise.resolve().then(() => bridge.getLaunchContext()).then((value) => {
      if (active && value?.entryPoint === 'tabbar') setEntryPoint('tabbar');
    }).catch(() => {});
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!navigator.geolocation) return;
    let active = true;
    navigator.geolocation.getCurrentPosition(({ coords }) => {
      if (!active) return;
      const detected = nearbyDemoCity(coords.latitude, coords.longitude);
      if (detected) changeCity(detected);
    }, () => {},
    { enableHighAccuracy: false, maximumAge: 600000, timeout: 8000 });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!panel) return;
    const close = (event) => { if (event.key === 'Escape') setPanel(null); };
    window.addEventListener('keydown', close);
    const back = window.WebApp?.BackButton;
    const onBack = () => setPanel(null);
    if (back?.show && back?.onClick) { back.show(); back.onClick(onBack); }
    return () => {
      window.removeEventListener('keydown', close);
      if (back?.offClick) back.offClick(onBack);
      if (back?.hide) back.hide();
    };
  }, [panel]);

  useEffect(() => {
    if (panel === 'lunch') return;
    lunchLocationRequest.current += 1;
    setLunchPoint(null);
  }, [panel]);

  function changeVariant(id) {
    if (id === variantID) return;
    setVariantID(id); setRouteOption(0); setSelectedID(null); setPanel(null); setProposal(null); setSaved(false); setRevision(1);
  }

  function proposeRebuild() {
    const choices = [0, 1, 2].filter((option) => option !== routeOption);
    const option = choices[Math.floor(Math.random() * choices.length)];
    const candidate = demoRebuiltVariant(baseVariant, city, option);
    const hasObligations = [...pinned, ...completed, ...reported, ...removed, ...pauses]
      .some((id) => variant.stops.some((stop) => stop.id === id));
    const strictConflict = pushkinMode === 'only' && !prototypePushkinEligible(candidate);
    setProposal({ kind: 'rebuild', candidate, option, conflicts: hasObligations || strictConflict,
      reason: hasObligations ? 'У текущего маршрута есть закреплённые точки или отметки. Их нельзя перенести на новую последовательность автоматически.'
        : strictConflict ? 'В новой последовательности платное культурное посещение не подходит под строгий режим карты.'
          : 'Альтернатива случайно выбрана из локального демонабора. База данных и optimizer пока не участвуют; переходы и доступность не проверены.' });
    setPanel('proposal');
  }

  function changeCity(next) {
    if (next === city) return;
    setCity(next); setRouteOption(0); setSelectedID(null); setPanel(null); setProposal(null);
    setPinned([]); setCompleted([]); setReported([]); setRemoved([]); setPauses([]); setShift(0); setLunches([]); setEditingLunchID(null); setDraftLunchAfterIndex(0); setLunchNavigation(null); setStartPlace(''); setFinishPlace(''); setStartPoint(null); setFinishPoint(null); setDraftStart(''); setDraftFinish(''); setDraftStartPoint(null); setDraftFinishPoint(null); setSaved(false); setRevision(1);
  }

  function openStop(id) { setSelectedID(id); setPanel('stop'); }
  function toggle(list, setList, id) {
    setList(list.includes(id) ? list.filter((item) => item !== id) : [...list, id]);
    setRevision((value) => value + 1);
  }

  function proposeEndpoints() {
    const start = draftStart.trim();
    const finish = draftFinish.trim();
    if (start === startPlace && finish === finishPlace && draftStartPoint === startPoint && draftFinishPoint === finishPoint) { setPanel(null); return; }
    setProposal({ kind: 'endpoints', start, finish, startPoint: draftStartPoint, finishPoint: draftFinishPoint, conflicts: false,
      reason: 'Точки показаны на карте. Время переходов и выполнимость расписания не проверены.' });
    setPanel('proposal');
  }

  function beginMapPick(mode) {
    setMapPickMode(mode);
    setPanel(null);
    requestAnimationFrame(() => mapSection.current?.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  }

  function acceptMapPoint(coordinates, address) {
    if (!mapPickMode) return;
    const place = address || `Точка ${coordinates.map((value) => value.toFixed(5)).join(', ')}`;
    const start = mapPickMode === 'start' ? place : startPlace;
    const finish = mapPickMode === 'finish' ? place : finishPlace;
    const nextStartPoint = mapPickMode === 'start' ? coordinates : startPoint;
    const nextFinishPoint = mapPickMode === 'finish' ? coordinates : finishPoint;
    setDraftStart(start); setDraftFinish(finish);
    setDraftStartPoint(nextStartPoint); setDraftFinishPoint(nextFinishPoint);
    setProposal({ kind: 'endpoints', start, finish, startPoint: nextStartPoint, finishPoint: nextFinishPoint, conflicts: false,
      reason: 'Выбранная точка пока не влияет на расписание. Пешеходный путь на карте не подтверждает время прибытия.' });
    setMapPickMode(null);
    setPanel('proposal');
  }

  function chooseLunch(cafe) {
    if (!availableLunchStops.length) return;
    const position = Math.min(draftLunchAfterIndex, availableLunchStops.length - 1);
    const afterID = availableLunchStops[position].id;
    const afterIndex = variant.stops.findIndex((stop) => stop.id === afterID);
    const offset = lunchDuration - 10;
    const candidateEnd = shiftClock(variant.times[2][1], shift + (position === availableLunchStops.length - 1 ? lunchDuration + 5 : offset));
    const conflicts = candidateEnd > '17:00' || variant.stops.slice(afterIndex + 1).some((stop) => pinned.includes(stop.id) || completed.includes(stop.id) || reported.includes(stop.id));
    const id = editingLunchID || window.crypto.randomUUID();
    const multiple = lunches.some((item) => item.id !== id);
    const entry = { id, afterIndex: position, cafe, duration: lunchDuration, offset: conflicts || multiple ? 0 : offset, needsReschedule: conflicts || multiple };
    setLunches((items) => [...items.filter((item) => item.id !== id), entry].map((item) => multiple ? { ...item, offset: 0, needsReschedule: true } : item));
    setLunchNavigation(cafe && lunchPoint ? { origin: lunchPoint, cafe } : null);
    setRevision((value) => value + 1);
    setPanel(null);
    setNotice(conflicts || multiple ? 'Обед добавлен в демоплан. Время требует пересчёта.' : 'Обед добавлен в демоплан. Время перехода и доступность не проверены.');
    if (cafe) requestAnimationFrame(() => mapSection.current?.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  }

  function openLunch(id = null) {
    const entry = lunches.find((item) => item.id === id);
    setEditingLunchID(entry?.id || null);
    setLunchDuration(entry?.duration || 45);
    const requestID = ++lunchLocationRequest.current;
    setCafes([]);
    setDraftLunchAfterIndex(Math.min(entry?.afterIndex ?? 0, Math.max(availableLunchStops.length - 1, 0)));
    setCafeError('');
    setLunchPoint(null);
    setCafeStatus('locating');
    setPanel('lunch');
    if (!mapApiKey) {
      setCafeStatus('error');
      setCafeError('Поиск кафе сейчас недоступен. Можно оставить свободное время.');
      return;
    }
    if (!navigator.geolocation) {
      setCafeStatus('error');
      setCafeError('Геолокация недоступна на этом устройстве. Можно оставить свободное время.');
      return;
    }
    try { navigator.geolocation.getCurrentPosition(({ coords }) => {
      if (requestID !== lunchLocationRequest.current) return;
      const point = [coords.latitude, coords.longitude];
      if (!point.every(Number.isFinite) || Math.abs(point[0]) > 90 || Math.abs(point[1]) > 180) {
        setCafeStatus('error');
        setCafeError('Не удалось определить положение. Попробуйте открыть обед ещё раз.');
        return;
      }
      setLunchPoint(point);
      setCafeStatus('loading');
    }, (error) => {
      if (requestID !== lunchLocationRequest.current) return;
      setCafeStatus('error');
      setCafeError(error.code === 1 ? 'Разрешите доступ к геолокации, чтобы увидеть кафе рядом с вами.' : 'Не удалось определить положение. Попробуйте открыть обед ещё раз.');
    }, { enableHighAccuracy: true, maximumAge: 0, timeout: 10000 }); } catch {
      setCafeStatus('error');
      setCafeError('Не удалось запросить геолокацию. Можно оставить свободное время.');
    }
  }

  function createDelayProposal() {
    const candidateEnd = shiftClock(finishTime, delayMinutes);
    const conflicts = candidateEnd > '17:00' || pinned.length > 0;
    setProposal({ kind: 'delay', minutes: delayMinutes, mode: delayMode, candidateEnd, conflicts,
      reason: conflicts ? 'Окно до 17:00 или закреплённая точка не позволяют принять пример автоматически.' : 'Модельный сдвиг свободных посещений. Время переходов и доступность не проверены.' });
    setPanel('proposal');
  }

  function createRemovalProposal() {
    setProposal({ kind: 'remove', stopID: selected.id, mode: removalMode,
      conflicts: selected.pinned || selected.reported,
      reason: selected.pinned || selected.reported ? 'Точка закреплена или участие отмечено. Сначала проверьте обязательства.' : 'Внешние билеты и регистрации этим действием не отменяются.' });
    setPanel('proposal');
  }

  function applyProposal() {
    if (!proposal || proposal.conflicts) return;
    if (proposal.kind === 'delay') setShift((value) => value + proposal.minutes);
    else if (proposal.kind === 'rebuild') { setRouteOption(proposal.option); setSelectedID(null); setSaved(false); }
    else if (proposal.kind === 'endpoints') { setStartPlace(proposal.start); setFinishPlace(proposal.finish); setStartPoint(proposal.startPoint); setFinishPoint(proposal.finishPoint); }
    else if (proposal.mode === 'pause') setPauses((value) => [...value, proposal.stopID]);
    else setRemoved((value) => [...value, proposal.stopID]);
    setRevision((value) => value + 1);
    setNotice('Модельное изменение применено только в этом прототипе.');
    setProposal(null); setPanel(null);
  }

  async function shareExample() {
    const link = `https://max.ru/t293_hakaton_max_bot?startapp=demo_shared_${scenarioKey}`;
    if (window.WebApp?.shareMaxContent) {
      try { await window.WebApp.shareMaxContent({ text: 'Демонстрационный маршрут «Импульс Города»', link }); setNotice('Открыт системный экран отправки MAX.'); return; } catch { /* Copy remains available. */ }
    }
    try { await navigator.clipboard.writeText(link); setNotice('Ссылка на демонстрационный пример скопирована.'); }
    catch { setNotice('Не удалось скопировать ссылку на этом устройстве.'); }
  }

  return (
    <main className={`workspace${entryPoint === 'tabbar' ? ' is-tabbar' : ''}`} style={{ '--route-accent': variant.color }}>
      <header className="workspace-topbar">
        <Brand className="workspace-wordmark" />
        <span className="workspace-demo-tag">Демо</span>
      </header>
      <div className="workspace-wrap">
        <div className="workspace-heading">
          <div className="workspace-title-block">
            <h1>{variant.title}</h1>
            <p>12:00—{finishTime} · {visible.length} точки</p>
          </div>
          <button type="button" className="workspace-settings" onClick={() => setPanel('settings')} aria-label="Настройки маршрута">Настроить</button>
        </div>
        <section className="workspace-variants" aria-label="Варианты маршрута">
          <div className="workspace-variants-head"><h2>Вариант дня</h2><button type="button" onClick={() => setPanel('compare')}>Сравнить</button></div>
          <div className="workspace-variant-scroll">
            {variants.map((option) => <button type="button" key={option.id}
              className={`workspace-variant${variantID === option.id ? ' is-active' : ''}`}
              onClick={() => changeVariant(option.id)} aria-pressed={variantID === option.id}>
              <strong>{option.title}</strong>
            </button>)}
          </div>
        </section>

        <div className="workspace-columns">
          <div ref={mapSection} className="workspace-map-column">
            <TwoGisRouteMap apiKey={mapApiKey} city={city} stops={stops} startPlace={startPlace} finishPlace={finishPlace}
              startPoint={startPoint} finishPoint={finishPoint} lunches={routeLunches} lunchNavigation={lunchNavigation} movement={movement} pickMode={mapPickMode}
              onPick={acceptMapPoint} onCancelPick={() => setMapPickMode(null)} selectedID={selected.id} onSelect={openStop}
              radius={panel === 'lunch' ? lunchRadius : 0} cafeCenter={panel === 'lunch' ? lunchPoint : null}
              onCafes={(results, error) => { setCafes(results); setCafeError(error || ''); setCafeStatus(error ? 'error' : results.length ? 'ready' : 'empty'); }} />
          </div>
          <section className="workspace-itinerary" aria-labelledby="workspace-itinerary-title">
            <div className="workspace-section-head"><h2 id="workspace-itinerary-title">По пути</h2><span>{visible.length} точки · версия {revision}</span></div>
            <div className="workspace-route-tools">
              <button type="button" onClick={() => setPanel('settings')}>Старт и финиш</button>
              <button type="button" onClick={() => setPanel('pushkin')}>Пушкинская карта</button>
            </div>
            {strictPushkinConflict && <p className="workspace-route-alert is-warning">Этот вариант конфликтует с режимом «Только по Пушкинской карте»: есть платная культурная точка без подтверждённой пригодности. Выберите другой вариант или измените режим.</p>}
            {(startPlace || finishPlace) && <p className="workspace-endpoints">{startPlace || 'Старт'} <span>→</span> {finishPlace || 'Финиш'} <button type="button" onClick={() => setPanel('settings')}>Изменить</button></p>}
            {cancelledStop && <p className="workspace-route-alert is-warning">Модельная отмена второй точки. Текущий маршрут не изменён. <button type="button" onClick={() => openStop(variant.stops[1].id)}>Посмотреть точку</button></p>}
            <ol className="workspace-timeline">
              {visible.map((stop, index) => <React.Fragment key={stop.id}><li>
                <button type="button" className={`workspace-visit${selected.id === stop.id ? ' is-active' : ''}${stop.completed ? ' is-completed' : ''}`}
                  onClick={() => openStop(stop.id)} aria-label={`${stop.start} ${stop.title}`}>
                  <span className="workspace-visit-time">{stop.start}</span>
                  <span className="workspace-visit-main"><small><span className="workspace-visit-marker" aria-hidden="true">{stop.completed ? '✓' : index + 1}</span>{stop.pause ? 'Пауза' : `${stop.category} · до ${stop.end}`}</small>
                    <strong>{stop.pause ? 'Пауза вместо посещения' : stop.title}</strong>
                    {(stop.completed || stop.reported || stop.pinned || stop.availability === 'registration_required') && <em>{stop.completed ? 'Пройдено' : stop.reported ? 'Участие отмечено' : stop.pinned ? 'Закреплено' : 'Нужна регистрация'}</em>}
                  </span><span className="workspace-visit-chevron" aria-hidden="true">›</span>
                </button>
              </li>
              {routeLunches.filter((item) => item.afterStopID === stop.id).map((lunch) => <li key={lunch.id} className="workspace-lunch-item"><button type="button" className="workspace-lunch-card" onClick={() => openLunch(lunch.id)} aria-label="Обед по пути: выбрать кафе рядом с вами">
                <span className="workspace-lunch-icon" aria-hidden="true">☕</span>
                <span><small>{lunch?.needsReschedule ? 'Время требует пересчёта' : lunch.duration ? `${shiftClock(stop.end, 5)}—${shiftClock(stop.end, 5 + lunch.duration)}` : 'После этой точки'}</small><strong>{lunch?.cafe ? `Обед · ${lunch.cafe.name}` : 'Свободное время на обед'}</strong><em>{lunch?.cafe ? `≈ ${lunch.cafe.distance} м на момент выбора` : 'Найти кафе рядом с вами'}</em></span>
                <span className="workspace-visit-chevron" aria-hidden="true">›</span>
              </button></li>)}
              {index < visible.length - 1 && <li className="workspace-leg-item"><span className="workspace-leg">Переход · время не проверено</span></li>}
              </React.Fragment>)}
            </ol>
            {visible.length > 0 && <button type="button" className="workspace-add-lunch" onClick={openLunch}>+ Добавить обед</button>}
            <p className="workspace-freshness">Демонстрационный маршрут. Время, доступность и стоимость требуют проверки.</p>
          </section>
        </div>

        <div className="workspace-actionbar">
          {readOnly ? <><span>Доступен только просмотр демонстрационного примера.</span><button type="button" className="workspace-primary" onClick={() => { setReadOnly(false); setNotice('Создана локальная копия примера без чужих отметок.'); setCompleted([]); setReported([]); setPinned([]); setRevision(1); }}>Создать свой пример</button></> : <>
            <button type="button" className="workspace-primary" onClick={proposeRebuild}>Перестроить</button>
            <button type="button" onClick={openLunch}>Обед</button>
            <button type="button" onClick={() => setPanel('delay')}>Опаздываю</button>
            <button type="button" onClick={() => { setSaved((value) => !value); setNotice(saved ? 'Удалено из сохранённого на устройстве.' : 'Сохранено на этом устройстве.'); }}>{saved ? 'Сохранено' : 'Сохранить'}</button>
          </>}
        </div>
        <div className="workspace-sharebar"><button type="button" onClick={() => setPanel('share')}>Поделиться маршрутом <span aria-hidden="true">↗</span></button><span>Демо · стоимость неизвестна</span></div>
        {notice && <p className="workspace-notice" role="status">{notice}</p>}
      </div>

      {panel === 'pushkin' && <Sheet title="Пушкинская карта" onClose={() => setPanel(null)}>
        <label className="workspace-field">Учитывать карту<select value={pushkinMode} onChange={(event) => setPushkinMode(event.target.value)}><option value="none">Нет</option><option value="prefer">Предпочитать подходящие события</option><option value="only">Только подходящие платные события</option></select></label>
        <p className="workspace-sheet-note">{conditionalPushkin ? `В этом примере возможная сумма оплаты картой — ${conditionalPushkin} ₽.` : 'Стоимость подходящих событий неизвестна.'} Пригодность событий не подтверждена. Еда и транспорт оплачиваются отдельно.</p>
        {strictPushkinConflict && <p className="workspace-conflict">Этот вариант не подходит для строгого режима карты. Выберите другой вариант.</p>}
        <button type="button" className="workspace-wide-primary" onClick={() => setPanel(null)}>Готово</button>
      </Sheet>}

      {panel === 'stop' && <Sheet title={selected.pause ? 'Свободное время' : selected.title} onClose={() => setPanel(null)}>
        <div className="workspace-sheet-eyebrow">{selected.category} · {selected.start}—{selected.end} · {demoCities[city].timezone}</div>
        <p>{selected.pause ? 'Это свободное окно после изменения примера.' : selected.description}</p>
        <dl className="workspace-details">
          <div><dt>Доступность</dt><dd>{cancelledStop && selected.id === variant.stops[1].id ? 'Модельная отмена; маршрут не перестроен' : selected.availability === 'registration_required' ? 'Нужно проверить регистрацию' : 'Неизвестна'}</dd></div>
          <div><dt>Стоимость</dt><dd>{selected.price || 'Неизвестна'}{selected.price && ' · фактическая цена не подтверждена'}</dd></div>
          <div><dt>Пушкинская карта</dt><dd>{selected.pushkinExample ? 'В демоданных возможна оплата; у площадки не подтверждена' : 'Применимость не подтверждена'}</dd></div>
          <div><dt>Источник</dt><dd>Синтетические данные · актуальность не установлена</dd></div>
          <div><dt>Участие</dt><dd>{selected.reported ? 'Отмечено вами; организатором не подтверждено' : 'Не подтверждено'}</dd></div>
        </dl>
        {!readOnly && !selected.pause && <div className="workspace-sheet-actions">
          <button type="button" onClick={() => toggle(pinned, setPinned, selected.id)}>{selected.pinned ? 'Снять закрепление' : 'Закрепить точку'}</button>
          <button type="button" onClick={() => toggle(completed, setCompleted, selected.id)}>{selected.completed ? 'Снять отметку' : 'Отметить выполненным'}</button>
          {selected.availability === 'registration_required' && <button type="button" onClick={() => toggle(reported, setReported, selected.id)}>{selected.reported ? 'Отменить свою отметку' : 'Я зарегистрировался'}</button>}
          <button type="button" onClick={() => setPanel('remove')}>Убрать из дня</button>
        </div>}
        <p className="workspace-sheet-note">Покупка, допуск и подтверждение организатора в демоверсии недоступны. Отметки сохраняются только на этом устройстве.</p>
      </Sheet>}

      {panel === 'lunch' && <Sheet title="Обед по пути" onClose={() => setPanel(null)}>
        <p className="workspace-lunch-intro">Кафе рядом с вами сейчас</p>
        <label className="workspace-field">После какой точки<select value={draftLunchAfterIndex} onChange={(event) => setDraftLunchAfterIndex(Number(event.target.value))}>{availableLunchStops.map((stop, index) => <option key={stop.id} value={index}>{index + 1}. {stop.title}</option>)}</select></label>
        <label className="workspace-field">Время на обед<select value={lunchDuration} onChange={(event) => setLunchDuration(Number(event.target.value))}><option value={45}>45 минут</option><option value={60}>60 минут</option></select></label>
        <label className="workspace-field">Искать в радиусе<select value={lunchRadius} onChange={(event) => { setLunchRadius(Number(event.target.value)); setCafes([]); setCafeError(''); if (lunchPoint) setCafeStatus('loading'); }}><option value={300}>300 м</option><option value={500}>500 м</option><option value={800}>800 м</option><option value={1000}>1000 м</option></select></label>
        <div className="workspace-cafe-list">
          {cafes.map((cafe) => <div className="workspace-cafe-card" key={cafe.id}>
            <button type="button" onClick={() => chooseLunch(cafe)}><span className="workspace-cafe-symbol" aria-hidden="true">{cafe.name.charAt(0).toLocaleUpperCase('ru')}</span><span className="workspace-cafe-copy"><strong>{cafe.name}</strong><small>≈ {cafe.distance} м по прямой · 2ГИС</small></span><span className="workspace-cafe-chevron" aria-hidden="true">›</span></button>
            {/^\d+$/.test(String(cafe.id)) && <a href={`https://2gis.ru/${city}/firm/${cafe.id}`} target="_blank" rel="noopener noreferrer" onClick={(event) => {
              if (window.WebApp?.openLink) { event.preventDefault(); window.WebApp.openLink(event.currentTarget.href); }
            }}>Открыть в 2ГИС ↗</a>}
          </div>)}
        </div>
        <p className="workspace-lunch-status" role="status">{!mapApiKey ? 'Поиск кафе недоступен. Можно оставить свободное время.' : cafeStatus === 'error' ? cafeError : cafeStatus === 'locating' ? 'Определяем ваше местоположение…' : cafeStatus === 'empty' ? 'Рядом кафе не нашлись. Попробуйте увеличить радиус.' : cafeStatus === 'loading' ? 'Ищем кафе рядом…' : 'Цена, часы работы и наличие мест не проверены. Выбор не бронирует столик.'}</p>
        {['error', 'empty'].includes(cafeStatus) && <button type="button" className="workspace-lunch-free-action" onClick={() => openLunch(editingLunchID)}>Повторить поиск рядом со мной</button>}
        {lunch && <button type="button" className="workspace-lunch-free-action" onClick={() => chooseLunch(lunch.cafe)}>Перенести выбранный обед сюда</button>}
        <button type="button" className="workspace-lunch-free-action" onClick={() => chooseLunch(null)}>Оставить свободное время без кафе</button>
        {lunch && <button type="button" className="workspace-remove-lunch" onClick={() => { setLunches((items) => items.filter((item) => item.id !== editingLunchID)); setLunchNavigation(null); setRevision((value) => value + 1); setPanel(null); setNotice('Обед убран. Его можно добавить после другой точки.'); }}>Убрать обед</button>}
      </Sheet>}

      {panel === 'more' && <Sheet title="Действия с примером" onClose={() => setPanel(null)}>
        <div className="workspace-sheet-actions">
          <button type="button" onClick={() => { setSaved((value) => !value); setNotice(saved ? 'Пример удалён из локального сохранения.' : 'Пример сохранён на этом устройстве.'); setPanel(null); }}>{saved ? 'Убрать из сохранённого' : 'Сохранить на устройстве'}</button>
          <button type="button" onClick={() => setPanel('share')}>Поделиться</button>
          <button type="button" onClick={() => setPanel('settings')}>Условия и Пушкинская карта</button>
        </div>
      </Sheet>}

      {panel === 'delay' && <Sheet title="Если вы опаздываете" onClose={() => setPanel(null)}>
        <p>Выберите ситуацию. Сначала покажем предложение; текущий план не меняется без вашего решения.</p>
        <div className="workspace-choice-list">
          <label><input type="radio" name="delay-mode" value="already_delayed" checked={delayMode === 'already_delayed'} onChange={() => setDelayMode('already_delayed')} /><span><strong>Я уже опоздал</strong><small>Продолжить от фактического времени; прошедшую задержку не прибавлять повторно.</small></span></label>
          <label><input type="radio" name="delay-mode" value="future_wait" checked={delayMode === 'future_wait'} onChange={() => setDelayMode('future_wait')} /><span><strong>Смогу продолжить позже</strong><small>Добавить ожидание к текущему времени.</small></span></label>
        </div>
        <label className="workspace-field">Минуты для модельного сравнения<select value={delayMinutes} onChange={(event) => setDelayMinutes(Number(event.target.value))}><option value={15}>15 минут</option><option value={30}>30 минут</option><option value={60}>60 минут</option></select></label>
        <button type="button" className="workspace-wide-primary" onClick={createDelayProposal}>Посмотреть изменения</button>
      </Sheet>}

      {panel === 'remove' && <Sheet title="Убрать точку" onClose={() => setPanel('stop')}>
        <p>Выберите, что делать с освободившимся временем. Внешние билеты и регистрации не отменяются.</p>
        <div className="workspace-choice-list">
          <label><input type="radio" name="removal-mode" checked={removalMode === 'pause'} onChange={() => setRemovalMode('pause')} /><span><strong>Оставить свободное время</strong><small>Следующие точки сохраняются на местах.</small></span></label>
          <label><input type="radio" name="removal-mode" checked={removalMode === 'rebuild'} onChange={() => setRemovalMode('rebuild')} /><span><strong>Убрать из примера</strong><small>Автоматический подбор замены пока недоступен.</small></span></label>
        </div>
        <button type="button" className="workspace-wide-primary" onClick={createRemovalProposal}>Сравнить до и после</button>
      </Sheet>}

      {panel === 'proposal' && proposal && <Sheet title="Предложение изменений" onClose={() => setPanel(null)}>
        <p>Текущая версия {revision} останется прежней, пока вы не примените предложение.</p>
        <div className="workspace-compare"><div><small>СЕЙЧАС</small><strong>{proposal.kind === 'delay' ? `Финиш ${finishTime}` : proposal.kind === 'rebuild' ? variant.title : proposal.kind === 'endpoints' ? `${startPlace || 'Старт не задан'} → ${finishPlace || 'Финиш не задан'}` : selected.title}</strong><span>{proposal.kind === 'rebuild' ? variant.stops.map((stop) => stop.title).join(' → ') : proposal.kind === 'remove' ? `${selected.start}—${selected.end}` : 'Текущий пример'}</span></div><div><small>ПРЕДЛОЖЕНО</small><strong>{proposal.kind === 'delay' ? `Финиш ${proposal.candidateEnd}` : proposal.kind === 'rebuild' ? `Другой маршрут · ${proposal.candidate.title}` : proposal.kind === 'endpoints' ? `${proposal.start || 'Старт не задан'} → ${proposal.finish || 'Финиш не задан'}` : proposal.mode === 'pause' ? 'Свободное время' : 'Точка убрана'}</strong><span>{proposal.kind === 'delay' ? proposal.mode === 'already_delayed' ? 'Без повторного прибавления прошедшей задержки' : `Ожидание ${proposal.minutes} мин` : proposal.kind === 'rebuild' ? proposal.candidate.stops.map((stop) => stop.title).join(' → ') : 'Модельное изменение'}</span></div></div>
        <p className={proposal.conflicts ? 'workspace-conflict' : 'workspace-sheet-note'}>{proposal.conflicts ? 'Конфликт: ' : 'Оговорка: '}{proposal.reason}</p>
        <div className="workspace-sheet-actions"><button type="button" onClick={() => { setProposal(null); setPanel(null); }}>Отклонить</button><button type="button" className="workspace-primary" disabled={proposal.conflicts} onClick={applyProposal}>Применить к демо</button></div>
      </Sheet>}

      {panel === 'settings' && <Sheet title="Условия примера" onClose={() => setPanel(null)}>
        <p>Настоящий сценарий начинается в боте. Здесь можно посмотреть, как маршрутный экран реагирует на условия.</p>
        <div className="workspace-map-point-actions">
          <button type="button" onClick={() => beginMapPick('start')}><span>Старт</span><strong>{draftStart || 'Выбрать на карте'}</strong></button>
          <button type="button" onClick={() => beginMapPick('finish')}><span>Финиш</span><strong>{draftFinish || 'Выбрать на карте'}</strong></button>
        </div>
        <details className="workspace-manual-endpoints"><summary>Карта недоступна? Ввести адрес</summary>
          <label className="workspace-field">Место старта<input value={draftStart} onChange={(event) => { setDraftStart(event.target.value); setDraftStartPoint(null); }} placeholder="Адрес или название места" /></label>
          <label className="workspace-field">Место финиша<input value={draftFinish} onChange={(event) => { setDraftFinish(event.target.value); setDraftFinishPoint(null); }} placeholder="Адрес или название места" /></label>
          <button type="button" className="workspace-wide-primary" onClick={proposeEndpoints}>Применить адреса</button>
        </details>
        <label className="workspace-field">Передвижение<select value={movement} onChange={(event) => setMovement(event.target.value)}><option value="walk">Пешком</option><option value="transit">Общественный транспорт</option><option value="car">Автомобиль / такси</option></select></label>
        <label className="workspace-field">Личный бюджет, ₽<input type="number" min="0" inputMode="numeric" value={budget} onChange={(event) => setBudget(event.target.value)} placeholder="Не указан" /></label>
        <label className="workspace-field">Пушкинская карта<select value={pushkinMode} onChange={(event) => setPushkinMode(event.target.value)}><option value="none">Не учитывать</option><option value="prefer">Предпочитать подходящие события</option><option value="only">Только подходящие платные события</option></select></label>
        {pushkinMode !== 'none' && <p className="workspace-sheet-note">Возможность оплаты в карточке — только модельный пример. Еда и транспорт картой не оплачиваются.</p>}
        <label className="workspace-checkbox"><input type="checkbox" checked={volunteerInterest} onChange={(event) => setVolunteerInterest(event.target.checked)} /><span>Мне интересно волонтёрство</span></label>
        <button type="button" onClick={openLunch}>Настроить обед и кафе</button>
        <label className="workspace-checkbox"><input type="checkbox" checked={cancelledStop} onChange={(event) => setCancelledStop(event.target.checked)} /><span>Показать пример отмены</span></label>
        <p className="workspace-sheet-note">Эти изменения не запускают optimizer. Время переходов, доступность, цены и соблюдение бюджета остаются непроверенными. Для автомобиля расчёт переходов пока недоступен.</p>
      </Sheet>}

      {panel === 'compare' && <Sheet title="Сравнение вариантов" onClose={() => setPanel(null)}>
        <p>Три разные модельные последовательности. Для всех одинаково: доступность, фактическая стоимость и переходы не проверены.</p>
        <div className="workspace-comparison">{variants.map((option) => <div key={option.id}>
          <strong>{option.title}</strong><p>{option.why}</p>
          <ol>{option.stops.map((stop) => <li key={stop.id}>{stop.title}</li>)}</ol>
          <small>12:00—{option.times[2][1]} · переходы ≈ {option.legs.reduce((sum, minutes) => sum + minutes, 0)} мин, модельная оценка</small>
          <button type="button" disabled={variantID === option.id} onClick={() => changeVariant(option.id)}>{variantID === option.id ? 'Открыт сейчас' : 'Открыть вариант'}</button>
        </div>)}</div>
      </Sheet>}

      {panel === 'share' && <Sheet title="Поделиться примером" onClose={() => setPanel(null)}>
        <p>Демонстрационная ссылка откроет только синтетический маршрут без ваших отметок и настроек.</p>
        <div className="workspace-share-preview"><small>ПОЛУЧАТЕЛЬ УВИДИТ</small><strong>{variant.title}</strong><span>{demoCities[city].name} · пример дня · данные вымышлены</span></div>
        <p className="workspace-sheet-note">Это не настоящий отзываемый доступ к сохранённому плану. Общий маршрут с токеном появится после готовности gateway.</p>
        <div className="workspace-sheet-actions"><button type="button" onClick={() => { setReadOnly(true); setPanel(null); }}>Просмотр получателя</button><button type="button" className="workspace-primary" onClick={shareExample}>Отправить демо в MAX</button></div>
      </Sheet>}
    </main>
  );
}
