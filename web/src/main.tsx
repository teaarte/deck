import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './touch.css';
import './i18n';
import { StrictMode, useCallback, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { AppShell, Burger, Button, CloseButton, Drawer, Group, MantineProvider, NavLink, PasswordInput, ScrollArea, Stack, Text, Title } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { useDisclosure, useLocalStorage, useMediaQuery } from '@mantine/hooks';
import { useTranslation } from 'react-i18next';
import { call, setToken, token } from './api';
import { theme, useLook } from './look';
import { href, ScreenView, type Route } from './screen';
import { setChatScreen } from './chat';

interface MenuEntry {
  project?: string;
  title: string;
  screens: { name: string; title: string }[];
}

function parse(hash: string): Route {
  const [kind, a, b, c] = hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
  if (kind === 'p' && a && b) return { project: a, screen: b, record: c ? Number(c) : undefined };
  if (kind === 'deck' && a) return { screen: a, record: b ? Number(b) : undefined };
  return { screen: 'cockpit' };
}

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

const CHAT: Route = { screen: 'chat' };

function App() {
  const { t } = useTranslation();
  const [opened, { toggle, close }] = useDisclosure();
  const [route, setRoute] = useState(() => parse(location.hash));
  const [menu, setMenu] = useState<MenuEntry[]>([]);
  const loadMenu = useCallback(() => {
    call<MenuEntry[]>('deck.menu').then(setMenu, () => setMenu([]));
  }, []);
  useEffect(() => {
    const on = () => { setRoute(parse(location.hash)); close(); };
    window.addEventListener('hashchange', on);
    loadMenu();
    return () => window.removeEventListener('hashchange', on);
  }, []);
  const current = href({ ...route, record: undefined });
  const [chat, setChat] = useLocalStorage({ key: 'deck-chat-open', defaultValue: false });
  const phone = useMediaQuery('(max-width: 48em)', window.matchMedia('(max-width: 48em)').matches, { getInitialValueInEffect: false });
  useEffect(() => setChatScreen(route), [route]);
  useEffect(() => {
    const open = () => setChat(true);
    window.addEventListener('deck-chat-open', open);
    return () => window.removeEventListener('deck-chat-open', open);
  }, []);
  const panel = <ScreenView route={CHAT} onProjects={loadMenu} />;
  return (
    <AppShell padding="md" header={{ height: 52 }} navbar={{ width: 260, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      aside={{ width: 420, breakpoint: 'sm', collapsed: { desktop: !chat, mobile: true } }}>
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group>
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" aria-label={t('nav.menu')} />
            <Title order={4}>deck</Title>
          </Group>
          <Button size="xs" variant={chat ? 'filled' : 'default'} onClick={() => setChat(!chat)} data-chat-toggle>{t('chat.master')}</Button>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="xs">
        <AppShell.Section grow component={ScrollArea}>
          {menu.map((m) => (
            <div key={m.project ?? ''}>
              <Text size="xs" c="dimmed" tt="uppercase" px="sm" pt="sm">{m.project ? m.title : t('nav.deck')}</Text>
              {m.screens.map((s) => {
                const to = href({ project: m.project, screen: s.name });
                return <NavLink key={s.name} href={to} label={s.title} active={to === current} />;
              })}
            </div>
          ))}
        </AppShell.Section>
      </AppShell.Navbar>
      <AppShell.Main>
        <ScreenView route={route} onProjects={loadMenu} />
      </AppShell.Main>
      {chat && !phone && <AppShell.Aside p="xs" data-chat-panel>{panel}</AppShell.Aside>}
      <Drawer opened={chat && !!phone} onClose={() => setChat(false)} size="100%" position="right" withCloseButton={false} padding="xs" data-chat-phone>
        <Group justify="flex-end"><CloseButton onClick={() => setChat(false)} aria-label={t('chat.close')} /></Group>
        {chat && phone && panel}
      </Drawer>
    </AppShell>
  );
}

if ('serviceWorker' in navigator) void navigator.serviceWorker.register('/sw.js').catch(() => undefined);

// Themed follows the system color scheme unless this device picked one, and the look this device keeps.
function Themed() {
  const look = useLook();
  const themed = useMemo(() => theme(look), [look]);
  return (
    <MantineProvider theme={themed} defaultColorScheme="auto">
      <Notifications />
      {token() ? <App /> : <TokenPrompt />}
    </MantineProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Themed />
  </StrictMode>,
);
