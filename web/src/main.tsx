import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './theme/touch.css';
import './lib/i18n';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Themed } from './app/Themed';
// Themes come last: they override the base variables and the glass surfaces of the same specificity.
import './theme/themes.css';

if ('serviceWorker' in navigator) void navigator.serviceWorker.register('/sw.js').catch(() => undefined);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Themed />
  </StrictMode>,
);
