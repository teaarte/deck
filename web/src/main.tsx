import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './theme/touch.css';
import './theme/themes.css';
import './lib/i18n';
import { StrictMode, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Button, MantineProvider, PasswordInput, Stack, Text, Title } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { setToken, token } from './lib/api';
import { theme, useLook } from './theme/look';
import { resolver } from './theme/palette';
import { Shell } from './shell';

// TokenPrompt takes the access token once on a device: an app on the home screen does not share the browser's storage.
function TokenPrompt() {
  const { t } = useTranslation();
  const [value, setValue] = useState('');
  return (
    <Stack maw={420} mx="auto" mt="xl" p="md">
      <Title order={3}>deck</Title>
      <Text size="sm">{t('token.why')}</Text>
      <PasswordInput label={t('token.label')} value={value} onChange={(e) => setValue(e.currentTarget.value)} data-token />
      <Button disabled={!value.trim()} onClick={() => { setToken(value.trim()); location.reload(); }}>{t('token.save')}</Button>
    </Stack>
  );
}

if ('serviceWorker' in navigator) void navigator.serviceWorker.register('/sw.js').catch(() => undefined);

// Themed follows the system color scheme unless this device picked one, and the look this device keeps.
function Themed() {
  const look = useLook();
  const themed = useMemo(() => theme(look), [look]);
  return (
    <MantineProvider theme={themed} defaultColorScheme="auto" cssVariablesResolver={resolver} forceColorScheme={look.theme === 'neon' ? 'dark' : undefined}>
      <Notifications />
      {token() ? <Shell /> : <TokenPrompt />}
    </MantineProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Themed />
  </StrictMode>,
);
