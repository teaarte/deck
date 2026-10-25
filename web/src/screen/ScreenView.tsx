import { memo, useRef } from 'react';
import { A2uiSurface } from '@a2ui/react/v0_9';
import { Alert } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Route } from '../app/router';
import { Pending } from '../ui/Pending';
import { useScreen } from './useScreen';

type Shown = { surface: NonNullable<ReturnType<typeof useScreen>['surface']>; key: string };

// ScreenView draws a screen of deck as the server describes it. While the next screen loads the one before stays,
// so a move does not blink; a first screen that is slow shows its skeleton.
export const ScreenView = memo(function ScreenView({ route, visible = true }: { route: Route; visible?: boolean }) {
  const { t } = useTranslation();
  const { surface, error, key } = useScreen(route, visible);
  const last = useRef<Shown>(undefined);
  if (error) return <Alert color="red" title={t('screen.failed')} data-screen-error>{error}</Alert>;
  if (surface) last.current = { surface, key };
  const shown = last.current;
  if (!shown) return <Pending text={t('state.loading')} lines={6} />;
  return (
    <div aria-busy={!surface || undefined} data-screen-view>
      <A2uiSurface key={shown.surface.id + shown.key} surface={shown.surface} />
    </div>
  );
});
