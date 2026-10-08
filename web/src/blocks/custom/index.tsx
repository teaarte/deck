import { Pending } from '../../ui/Pending';
import { lazy, Suspense } from 'react';
import type { CustomValue } from '../../lib/catalog.gen';
import { blockImpl } from '../blockImpl';

const Custom = lazy(() => import('./Custom').then((m) => ({ default: m.Custom })));

export const custom = blockImpl('custom', (_, d) => <Suspense fallback={<Pending />}><Custom value={d.value as CustomValue} /></Suspense>);
