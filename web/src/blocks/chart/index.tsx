import { lazy, Suspense } from 'react';
import { Loader } from '@mantine/core';
import type { ChartValue } from '../../lib/catalog.gen';
import type { Shown } from '../../lib/format';
import { blockImpl } from '../blockImpl';

const Chart = lazy(() => import('./Chart').then((m) => ({ default: m.Chart })));

export const chart = blockImpl<Shown & { kind?: string }>('chart', (p, d) => (
  <Suspense fallback={<Loader size="xs" />}><div data-chart-block><Chart value={d.value as ChartValue} kind={p.kind} s={p} /></div></Suspense>
));
