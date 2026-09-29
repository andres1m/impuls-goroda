import React from 'react';
import { reviewExtraction } from './scenarioExtraction.js';

export default function ExtractionReview({ input, choice, disabled, onApply, onManual }) {
  let proposal;
  try { proposal = reviewExtraction(input); }
  catch (failure) { return <section className="scenario-card"><h2>Проверка условий</h2><p>{failure.message}</p><button type="button" className="scenario-option" disabled={disabled} onClick={onManual}>Заполнить самостоятельно</button></section>; }
  if (choice) return <p className="scenario-description" role="status">{choice === 'used' ? 'Предложение перенесено в форму. Проверьте поля перед расчётом.' : 'Вы заполняете условия самостоятельно.'} Исходное предложение хранится отдельно до завершения сценария.</p>;
  return <section className="scenario-card" aria-label="Предложенные условия">
    <h2>Так мы поняли ваш текст</h2>
    <dl>{proposal.rows.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
    <p>Проверьте предложение. В форму перенесутся только перечисленные условия; старт, финиш и время останутся прежними.</p>
    <div className="scenario-choices">
      <button type="button" className="scenario-option" disabled={disabled} onClick={() => onApply(proposal)}>Перенести в форму</button>
      <button type="button" className="scenario-option" disabled={disabled} onClick={onManual}>Заполнить самостоятельно</button>
    </div>
    <p className="scenario-timezone">Дата, время и неоднозначные пожелания требуют уточнения ниже. Расчёт начнётся только после вашего подтверждения.</p>
  </section>;
}
