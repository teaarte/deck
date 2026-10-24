import type { ReactNode } from 'react';
import { ActionIcon, Group, Tooltip } from '@mantine/core';
import { IconArrowLeft, IconLayoutGrid } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './shell.module.css';
import { back, go, href, useCanGoBack, useRoute } from './router';

// BackButton leads from an open record back to its screen.
export function BackButton() {
  const { t } = useTranslation();
  const route = useRoute();
  const trail = useCanGoBack();
  if (!route.record) return null;
  return (
    <Tooltip label={t('nav.back')}>
      <ActionIcon variant="default" size="lg" onClick={() => (trail ? back() : go({ project: route.project, screen: route.screen }))}
        aria-label={t('nav.back')} data-back>
        <IconArrowLeft size={18} />
      </ActionIcon>
    </Tooltip>
  );
}

// Home is the way to the tiles of deck from any other screen.
export function Home() {
  const { t } = useTranslation();
  const route = useRoute();
  if (!route.project && route.screen === 'apps') return null;
  return (
    <Tooltip label={t('nav.apps')}>
      <ActionIcon component="a" href={href({ screen: 'apps' })} variant="default" size={40} aria-label={t('nav.apps')} data-apps-home>
        <IconLayoutGrid size={18} />
      </ActionIcon>
    </Tooltip>
  );
}

// Toolbar is the head of a screen on a wide screen: the way to the tiles and back from a record, and the master.
export function Toolbar({ master }: { master: ReactNode }) {
  return (
    <div className={s.toolbar} data-toolbar>
      <Group gap="xs" wrap="nowrap" miw={0}>
        <Home />
        <BackButton />
      </Group>
      {master}
    </div>
  );
}
