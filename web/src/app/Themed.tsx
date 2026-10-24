import { useMemo } from 'react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { token } from '../lib/api';
import { theme, useLook } from '../theme/look';
import '../theme/base.css';
import { resolver } from '../theme/palette';
import { forced, useWear, useWorn } from '../theme/worn';
import { UndoToast } from '../ui/actions/undo';
import { Shell } from './Shell';
import { TokenPrompt } from './TokenPrompt';

// Themed wears the theme of the whole deck with what this device keeps, and follows the system color scheme unless
// the device or the theme picked one.
export function Themed() {
  const look = useLook();
  const signed = !!token();
  useWear(signed);
  const worn = useWorn();
  const themed = useMemo(() => theme(look, worn), [look.accent, look.density, look.font, worn]);
  return (
    <MantineProvider theme={themed} defaultColorScheme="auto" cssVariablesResolver={resolver} forceColorScheme={forced(worn)}>
      <Notifications position="top-center" />
      <UndoToast />
      {signed ? <Shell /> : <TokenPrompt />}
    </MantineProvider>
  );
}
