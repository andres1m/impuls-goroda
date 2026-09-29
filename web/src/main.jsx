import React from 'react';
import { createRoot } from 'react-dom/client';
import { MaxUI } from '@maxhub/max-ui';
import '@maxhub/max-ui/dist/styles.css';
import App from './App.jsx';
import DevPreview from './DevPreview.jsx';
import './styles.css';

const useDevPreview = Boolean(import.meta.env?.DEV) && new URLSearchParams(window.location.search).get('prod') !== '1';
const root = createRoot(document.getElementById('root'));
root.render(<MaxUI>{useDevPreview ? <DevPreview /> : <App />}</MaxUI>);

