import React from 'react';
import { createRoot } from 'react-dom/client';
import { MaxUI } from '@maxhub/max-ui';
import '@maxhub/max-ui/dist/styles.css';
import App from './App.jsx';
import './styles.css';

const root = createRoot(document.getElementById('root'));
root.render(<MaxUI><App /></MaxUI>);
