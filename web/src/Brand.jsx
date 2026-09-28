import React from 'react';

export default function Brand({ className = '', children = 'ИМПУЛЬС ГОРОДА' }) {
  return <span className={`app-brand ${className}`}>
    <svg className="app-brand-logo" viewBox="385 305 480 622" aria-hidden="true" focusable="false">
      <image href="/logo.png" width="1254" height="1254" />
    </svg>
    <span>{children}</span>
  </span>;
}
