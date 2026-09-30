import React, { useRef, useState } from 'react';

export default function ShareControls({ link, disabled, onCreate, onRevoke }) {
  const input = useRef(null);
  const [message, setMessage] = useState('');
  const active = useRef(true);
  const operation = useRef(0);
  const lastShareAt = useRef(0);
  React.useEffect(() => {
    active.current = true;
    operation.current += 1;
    setMessage('');
    return () => { active.current = false; operation.current += 1; };
  }, [link]);

  function shareInMax() {
    if (disabled || !link || Date.now() - lastShareAt.current < 750) return;
    const bridge = window.WebApp;
    if (typeof bridge?.shareMaxContent !== 'function') {
      setMessage('Отправка через MAX недоступна. Скопируйте ссылку ниже.');
      return;
    }
    lastShareAt.current = Date.now();
    const currentOperation = ++operation.current;
    setMessage('');
    try {
      const result = bridge.shareMaxContent({ text: 'Маршрут «Импульс города»', link });
      setMessage('Проверьте экран отправки MAX. Если он не открылся, скопируйте ссылку.');
      Promise.resolve(result).catch(() => {
        if (active.current && currentOperation === operation.current) setMessage('Не удалось открыть отправку в MAX. Скопируйте ссылку ниже.');
      });
    } catch {
      setMessage('Не удалось открыть отправку в MAX. Скопируйте ссылку ниже.');
    }
  }

  async function copyLink() {
    if (disabled || !link) return;
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
      <div className="server-participation-actions">
        {typeof window.WebApp?.shareMaxContent === 'function' && <button className="scenario-option" disabled={disabled} onClick={shareInMax}>Отправить в MAX</button>}
        <button className="scenario-option" disabled={disabled} onClick={copyLink}>Скопировать ссылку</button>
      </div>
      {message && <span role="status">{message}</span>}
    </>}
  </section>;
}
