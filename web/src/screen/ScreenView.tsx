import { memo, useEffect, useRef, useState } from 'react';
import { A2uiSurface } from '@a2ui/react/v0_9';
import { Alert, Loader } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Route } from '../app/router';
import { useScreen } from './useScreen';

type Shown = { surface: NonNullable<ReturnType<typeof useScreen>['surface']>; key: string };

// ScreenView draws a screen of deck as the server describes it. While the next screen loads the one before stays,
// so a move does not blink; a first screen that is slow shows a loader.
export const ScreenView = memo(function ScreenView({ route }: { route: Route }) {
  const { t } = useTranslation();
  const { surface, error, key } = useScreen(route);
  const last = useRef<Shown>(undefined);
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    if (surface) return;
    const timer = setTimeout(() => setSlow(true), 500);
    return () => { clearTimeout(timer); setSlow(false); };
  }, [surface]);
  if (error) return <Alert color="red" title={t('screen.failed')} data-screen-error>{error}</Alert>;
  if (surface) last.current = { surface, key };
  const shown = last.current;
  if (!shown) return slow ? <Loader /> : null;
  return (
    <div aria-busy={!surface || undefined} data-screen-view>
      <A2uiSurface key={shown.surface.id + shown.key} surface={shown.surface} />
    </div>
  );
});
