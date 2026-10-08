import { Pending } from '../../ui/Pending';
import { lazy, Suspense } from 'react';
import type { ChartValue } from '../../lib/catalog.gen';
import type { Shown } from '../../lib/format';
import { blockImpl } from '../blockImpl';

const Chart = lazy(() => import('./Chart').then((m) => ({ default: m.Chart })));

export const chart = blockImpl<Shown & { kind?: string }>('chart', (p, d) => (
  <Suspense fallback={<Pending />}><div data-chart-block><Chart value={d.value as ChartValue} kind={p.kind} s={p} /></div></Suspense>
));
