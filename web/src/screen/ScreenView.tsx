import { memo } from 'react';
import { A2uiSurface } from '@a2ui/react/v0_9';
import { Alert, Loader } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Route } from '../router';
import { ScreenActionsContext } from './ScreenActions';
import { useScreen } from './useScreen';

// ScreenView draws a screen of deck as the server describes it, and gives its blocks what they do.
export const ScreenView = memo(function ScreenView({ route, onProjects }: { route: Route; onProjects: () => void }) {
  const { t } = useTranslation();
  const { surface, error, key, actions } = useScreen(route, onProjects);
  if (error) return <Alert color="red" title={t('screen.failed')} data-screen-error>{error}</Alert>;
  if (!surface) return <Loader />;
  return (
    <ScreenActionsContext.Provider value={actions}>
      <A2uiSurface key={surface.id + key} surface={surface} />
    </ScreenActionsContext.Provider>
  );
});
