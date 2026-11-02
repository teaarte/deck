import { Appearance } from '../settings/Appearance';
import { blockImpl } from './blockImpl';

// appearance is how deck looks on this device, or with part pushes the pushes of the device.
export const appearance = blockImpl<{ part?: string }>('appearance', (p, d) => <Appearance part={p.part} actions={d.actions ?? []} />);
