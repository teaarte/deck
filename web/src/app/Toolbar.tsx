import type { ReactNode } from 'react';
import { ActionIcon, Group, Tooltip } from '@mantine/core';
import { IconArrowLeft, IconLayoutGrid, IconLayoutSidebarLeftExpand } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import s from './shell.module.css';
import { Bell } from './Bell';
import { back, href, useCanGoBack, useRoute } from './router';

// BackButton goes back where there is a way back, never on the cockpit or the cards, the roots of deck.
export function BackButton() {
  const { t } = useTranslation();
  const route = useRoute();
  const root = !route.project && (route.screen === 'cockpit' || route.screen === 'apps') && !route.record;
  if (!useCanGoBack() || root) return null;
  return (
    <Tooltip label={t('nav.back')}>
      <ActionIcon variant="default" radius="xl" size="lg" onClick={back} aria-label={t('nav.back')} data-back>
        <IconArrowLeft size={18} />
      </ActionIcon>
    </Tooltip>
  );
}

// Crumb names the project the screen belongs to.
export function Crumb({ entry }: { entry?: MenuEntry }) {
  if (!entry?.project) return null;
  return <span className={s.crumb}><NamedIcon name={entry.icon} fallback={entry.title} size={16} />{entry.title}</span>;
}

// Home is the way to the cards of deck when they stand instead of the side menu.
export function Home({ size = 'lg' }: { size?: 'lg' }) {
  const { t } = useTranslation();
  const route = useRoute();
  if (!route.project && route.screen === 'apps') return null;
  return (
    <Tooltip label={t('nav.apps')}>
      <ActionIcon component="a" href={href({ screen: 'apps' })} variant="default" radius="xl" size={size} aria-label={t('nav.apps')} data-apps-home>
        <IconLayoutGrid size={18} />
      </ActionIcon>
    </Tooltip>
  );
}

// Toolbar is the row above a screen on a wide screen: the menu's switch while it is hidden, with the bell then, or the
// way to the cards, the way back, the project, and the master.
export function Toolbar({ entry, navOpen, onShowNav, master, cards }: { entry?: MenuEntry; navOpen: boolean; onShowNav: () => void; master: ReactNode; cards?: boolean }) {
  const { t } = useTranslation();
  return (
    <div className={s.toolbar}>
      <Group gap="xs" wrap="nowrap" miw={0}>
        {cards && <Home />}
        {!navOpen && !cards && (
          <Tooltip label={t('nav.show')}>
            <ActionIcon variant="default" radius="xl" size="lg" onClick={onShowNav} aria-label={t('nav.show')} data-nav-show>
              <IconLayoutSidebarLeftExpand size={18} />
            </ActionIcon>
          </Tooltip>
        )}
        <BackButton />
        <Crumb entry={entry} />
      </Group>
      {navOpen ? master : <Group gap={4} wrap="nowrap"><Bell />{master}</Group>}
    </div>
  );
}
