import React, { useEffect, useRef, useState } from 'react';
import { Button } from '@maxhub/max-ui';
import { cities, validatePlanInput, validateStartInput } from './input.js';
import { categories, interests, movementModes } from './taxonomy.js';

const steps = ['Время и старт', 'Интересы и ограничения', 'Проверка'];
const shortSteps = ['Время', 'Интересы', 'Проверка'];

function initialForm() {
  return {
    city: 'moscow', startAt: '', endAt: '', latitude: '', longitude: '',
    interests: [], movementModes: [], excludedCategories: [],
  };
}

function selectedLabels(selected, options) {
  return options.filter(([code]) => selected.includes(code)).map(([, label]) => label).join(', ');
}

function readableTime(value) {
  return `${value.slice(8, 10)}.${value.slice(5, 7)}.${value.slice(0, 4)}, ${value.slice(11, 16)} UTC${value.slice(-6)}`;
}

export default function PlanForm() {
  const [form, setForm] = useState(initialForm);
  const [step, setStep] = useState(0);
  const [error, setError] = useState(null);
  const [review, setReview] = useState(null);
  const errorRef = useRef(null);
  const headingRef = useRef(null);
  const navigationRef = useRef(false);

  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  useEffect(() => {
    if (navigationRef.current) headingRef.current?.focus();
  }, [step]);

  function update(field, value) {
    setForm((current) => ({ ...current, [field]: value }));
    setError(null);
    setReview(null);
  }

  function toggleSelection(field, code) {
    setForm((current) => ({
      ...current,
      [field]: current[field].includes(code)
        ? current[field].filter((item) => item !== code)
        : [...current[field], code],
    }));
    setError(null);
    setReview(null);
  }

  function advance(event) {
    event.preventDefault();
    try {
      if (step === 0) validateStartInput(form);
      if (step === 1) setReview(validatePlanInput(form));
      setError(null);
      navigationRef.current = true;
      setStep((current) => Math.min(current + 1, steps.length - 1));
    } catch (validationError) {
      setError({ field: validationError.field, message: validationError.message });
    }
  }

  function back() {
    setError(null);
    navigationRef.current = true;
    setStep((current) => Math.max(current - 1, 0));
  }

  function fieldError(field) {
    return error?.field === field && (
      <p id={`error-${field}`} className="field-error" role="alert" tabIndex={-1} ref={errorRef}>
        {error.message}
      </p>
    );
  }

  function choiceGroup(field, title, options, hint) {
    return (
      <fieldset className="choices">
        <legend>{title}</legend>
        {hint && <p>{hint}</p>}
        <div className="interest-grid">
          {options.map(([code, label]) => (
            <label key={code} className={form[field].includes(code) ? 'interest selected' : 'interest'}>
              <input type="checkbox" checked={form[field].includes(code)} aria-describedby={error?.field === field ? `error-${field}` : undefined} onChange={() => toggleSelection(field, code)} />
              <span>{label}</span>
            </label>
          ))}
        </div>
        {fieldError(field)}
      </fieldset>
    );
  }

  return (
    <form className="form-card" onSubmit={advance} noValidate>
      <div className="form-topline">
        <span>Условия маршрута</span>
        <span>Шаг {step + 1} из {steps.length}</span>
      </div>
      <nav aria-label="Шаги ввода условий">
        <ol className="step-list">
          {steps.map((label, index) => (
            <li key={label} className={index === step ? 'active' : ''} aria-current={index === step ? 'step' : undefined}>
              <span className="step-index">{index + 1}</span>
              <span className="step-label"><span className="step-label-full">{label}</span><span className="step-label-short">{shortSteps[index]}</span></span>
            </li>
          ))}
        </ol>
      </nav>

      <div className="section-title">
        <span className="step-number">{String(step + 1).padStart(2, '0')}</span>
        <div>
          <h2 tabIndex={-1} ref={headingRef}>{steps[step]}</h2>
          <p>{step === 0 ? 'Укажите город, время и удобную точку старта.' : step === 1 ? 'Выберите то, что важно для прогулки.' : 'Проверьте введённые условия.'}</p>
        </div>
      </div>

      {step === 0 && (
        <>
          <fieldset className="city-choice">
            <legend>Город</legend>
            <div className="city-options">
              {Object.entries(cities).map(([code, city]) => (
                <label key={code} className={form.city === code ? 'city-option selected' : 'city-option'}>
                  <input type="radio" name="city" value={code} checked={form.city === code} onChange={() => update('city', code)} />
                  <span>{city.name}</span>
                </label>
              ))}
            </div>
            {fieldError('city')}
            <p className="timezone">Часовой пояс: {cities[form.city].timezone}</p>
          </fieldset>
          <div className="fields">
            <label htmlFor="startAt"><span>Начало</span>
              <input id="startAt" type="datetime-local" required value={form.startAt} aria-invalid={error?.field === 'startAt'} aria-describedby={error?.field === 'startAt' ? 'error-startAt' : undefined} onChange={(event) => update('startAt', event.target.value)} />
            </label>
            {fieldError('startAt')}
            <label htmlFor="endAt"><span>Конец</span>
              <input id="endAt" type="datetime-local" required min={form.startAt} value={form.endAt} aria-invalid={error?.field === 'endAt'} aria-describedby={error?.field === 'endAt' ? 'error-endAt' : undefined} onChange={(event) => update('endAt', event.target.value)} />
            </label>
            {fieldError('endAt')}
          </div>

          <fieldset className="origin-fields">
            <legend>Точка старта</legend>
            <p>Укажите координаты удобного места. Ручной старт работает и без геолокации.</p>
            <div className="fields two-columns">
              <label htmlFor="latitude"><span>Широта</span>
                <input id="latitude" type="number" inputMode="decimal" min="-90" max="90" step="any" required placeholder="55.75" value={form.latitude} aria-invalid={error?.field === 'latitude'} aria-describedby={error?.field === 'latitude' ? 'error-latitude' : undefined} onChange={(event) => update('latitude', event.target.value)} />
                {fieldError('latitude')}
              </label>
              <label htmlFor="longitude"><span>Долгота</span>
                <input id="longitude" type="number" inputMode="decimal" min="-180" max="180" step="any" required placeholder="37.62" value={form.longitude} aria-invalid={error?.field === 'longitude'} aria-describedby={error?.field === 'longitude' ? 'error-longitude' : undefined} onChange={(event) => update('longitude', event.target.value)} />
                {fieldError('longitude')}
              </label>
            </div>
          </fieldset>
        </>
      )}

      {step === 1 && (
        <>
          {choiceGroup('movementModes', 'Способы передвижения', movementModes, 'Выберите хотя бы один способ.')}
          {choiceGroup('interests', 'Что интересно', interests)}
          {choiceGroup('excludedCategories', 'Исключить категории', categories, 'Можно оставить пустым, если ограничений нет.')}
        </>
      )}

      {step === 2 && review && (
        <section className="review" aria-label="Проверка условий">
          <dl>
            <div><dt>Город</dt><dd>{cities[review.city].name}</dd></div>
            <div><dt>Часовой пояс</dt><dd>{review.timezone}</dd></div>
            <div><dt>Время</dt><dd>{readableTime(review.startAt)} — {readableTime(review.endAt)}</dd></div>
            <div><dt>Старт</dt><dd>{review.origin.latitude}, {review.origin.longitude}</dd></div>
            <div><dt>Передвижение</dt><dd>{selectedLabels(review.movementModes, movementModes)}</dd></div>
            <div><dt>Интересы</dt><dd>{selectedLabels(form.interests, interests) || 'Без предпочтений'}</dd></div>
            <div><dt>Исключены</dt><dd>{selectedLabels(review.excludedCategories, categories) || 'Нет'}</dd></div>
          </dl>
          <p className="review-note">Маршрут пока не строится. Вы можете вернуться и изменить условия.</p>
        </section>
      )}

      <div className="form-actions">
        {step > 0 && <button type="button" className="back-button" onClick={back}>Назад</button>}
        {step < 2 && <Button type="submit" mode="primary">{step === 0 ? 'К ограничениям' : 'Проверить условия'}</Button>}
      </div>
    </form>
  );
}
