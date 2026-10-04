import { lazy, Suspense } from 'react';
import { Loader } from '@mantine/core';
import type { CustomValue } from '../../lib/catalog.gen';
import { blockImpl } from '../blockImpl';

const Custom = lazy(() => import('./Custom').then((m) => ({ default: m.Custom })));

export const custom = blockImpl('custom', (_, d) => <Suspense fallback={<Loader size="xs" />}><Custom value={d.value as CustomValue} /></Suspense>);
