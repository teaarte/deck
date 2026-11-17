import { memo, useRef } from 'react';
import { A2uiSurface } from '@a2ui/react/v0_9';
import { Alert, Button } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Route } from '../app/router';
import { tally } from '../lib/tally';
import { Pending } from '../ui/Pending';
import { useScreen } from './useScreen';

type Shown = { surface: NonNullable<ReturnType<typeof useScreen>['surface']>; key: string };

// ScreenView draws a screen of deck as the server describes it. While the next screen loads the one before stays,
// so a move does not blink; a first screen that is slow shows its skeleton.
export const ScreenView = memo(function ScreenView({ route, visible = true }: { route: Route; visible?: boolean }) {
  const { t } = useTranslation();
  const { surface, error, slow, retry, key } = useScreen(route, visible);
  tally('screen');
  const last = useRef<Shown>(undefined);
  const again = <Button size="xs" variant="light" mt="xs" onClick={retry} data-screen-retry>{t('Retry')}</Button>;
  if (error) return <Alert color="red" title={t('The screen could not be shown')} data-screen-error>{error}<div>{again}</div></Alert>;
  if (surface) last.current = { surface, key };
  const shown = last.current;
  // A screen deck is slow to give says so, over the skeleton or the screen before.
  const late = slow && !surface && (
    <Alert color="yellow" title={t('deck is slow to answer')} mb="sm" data-screen-slow>{t('The screen is still loading; deck answers it when it can.')}<div>{again}</div></Alert>);
  if (!shown) return late || <Pending text={t('Loading…')} lines={6} />;
  return (
    <div aria-busy={!surface || undefined} data-screen-view>
      {late}
      <A2uiSurface key={shown.surface.id + shown.key} surface={shown.surface} />
    </div>
  );
});
