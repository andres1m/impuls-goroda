import { useEffect } from 'react';

export default function useClosingConfirmation(required) {
  useEffect(() => {
    if (!required) return;
    const warn = (event) => { event.preventDefault(); event.returnValue = ''; };
    window.addEventListener('beforeunload', warn);
    const bridge = window.WebApp;
    const supported = typeof bridge?.enableClosingConfirmation === 'function'
      && typeof bridge?.disableClosingConfirmation === 'function';
    if (supported) {
      try { bridge.enableClosingConfirmation(); }
      catch { /* Server draft saving remains available without native confirmation. */ }
    }
    return () => {
      window.removeEventListener('beforeunload', warn);
      if (supported) {
        try { bridge.disableClosingConfirmation(); }
        catch { /* Bridge failures must not interrupt leaving the form. */ }
      }
    };
  }, [required]);
}
