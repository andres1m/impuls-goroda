import React, { useRef, useState } from 'react';

export default function ShareControls({ link, disabled, onCreate, onRevoke }) {
  const input = useRef(null);
  const [message, setMessage] = useState('');
  const active = useRef(true);
  React.useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  async function copyLink() {
    setMessage('');
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
      await navigator.clipboard.writeText(link);
      if (active.current) setMessage('Ссылка скопирована');
    } catch {
      if (!active.current) return;
      input.current?.focus(); input.current?.select();
      setMessage('Выделите и скопируйте ссылку вручную');
    }
  }

  return <section className="owner-route-actions owner-share-controls" aria-labelledby="owner-share-title">
    <h2 id="owner-share-title">Поделиться маршрутом</h2>
    <p className="owner-share-hint">Ссылку сможет открыть любой, кому вы её отправите. Новая ссылка заменит предыдущую.</p>
    <div className="server-participation-actions">
      <button className="scenario-option" disabled={disabled} onClick={onCreate}>{link ? 'Создать новую ссылку' : 'Создать ссылку'}</button>
      <button className="scenario-option" disabled={disabled} onClick={onRevoke}>Отозвать ссылку</button>
    </div>
    {link && <>
      <label className="owner-share-label">Ссылка на маршрут
        <input ref={input} type="text" value={link} readOnly autoComplete="off" spellCheck={false} onFocus={(event) => event.target.select()} />
      </label>
      <button className="scenario-option" disabled={disabled} onClick={copyLink}>Скопировать ссылку</button>
      {message && <span role="status">{message}</span>}
    </>}
  </section>;
}
