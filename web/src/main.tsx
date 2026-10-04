import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './theme/touch.css';
import './theme/themes.css';
import './lib/i18n';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Themed } from './app/Themed';

if ('serviceWorker' in navigator) void navigator.serviceWorker.register('/sw.js').catch(() => undefined);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Themed />
  </StrictMode>,
);
