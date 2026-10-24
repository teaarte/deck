import type { ReactNode } from 'react';
import { ActionIcon, Group } from '@mantine/core';
import { IconMenu2 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import s from './shell.module.css';
import { Bell } from './Bell';
import { Logo } from './Logo';
import { BackButton, Crumb, Home } from './Toolbar';

// PhoneBar is the bar on top of a phone: the menu or the way to the cards, the way back, the project or the logo, the
// bell and the master.
export function PhoneBar({ entry, onMenu, master, cards }: { entry?: MenuEntry; onMenu: () => void; master: ReactNode; cards?: boolean }) {
  const { t } = useTranslation();
  return (
    <header className={s.phoneBar} data-phone-bar>
      <Group gap={6} wrap="nowrap" miw={0}>
        {cards ? <Home /> : (
          <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" onClick={onMenu} aria-label={t('nav.menu')} data-nav-open>
            <IconMenu2 size={20} />
          </ActionIcon>
        )}
        <BackButton />
        {entry?.project ? <Crumb entry={entry} /> : <Logo />}
      </Group>
      <Group gap={4} wrap="nowrap"><Bell />{master}</Group>
    </header>
  );
}
