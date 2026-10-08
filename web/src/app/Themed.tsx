import { useMemo } from 'react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { token } from '../lib/api';
import { theme, useLook } from '../theme/look';
import '../theme/base.css';
import { resolver } from '../theme/palette';
import { UndoToast } from '../ui/undo';
import { Shell } from './Shell';
import { TokenPrompt } from './TokenPrompt';

// Themed follows the system color scheme unless this device picked one, and the look this device keeps.
export function Themed() {
  const look = useLook();
  const themed = useMemo(() => theme(look), [look.accent, look.density, look.font, look.radius, look.theme]);
  return (
    <MantineProvider theme={themed} defaultColorScheme="auto" cssVariablesResolver={resolver} forceColorScheme={look.theme === 'neon' ? 'dark' : undefined}>
      <Notifications position="top-center" />
      <UndoToast />
      {token() ? <Shell /> : <TokenPrompt />}
    </MantineProvider>
  );
}
