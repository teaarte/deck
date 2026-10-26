import { Pending } from '../../ui/Pending';
import { lazy, Suspense, useContext } from 'react';
import { Fit } from '../around';
import type { ChartValue } from '../../lib/catalog.gen';
import type { Shown } from '../../lib/format';
import { blockImpl } from '../blockImpl';

const Chart = lazy(() => import('./Chart').then((m) => ({ default: m.Chart })));

export const chart = blockImpl<Shown & { kind?: string }>('chart', (p, d) => <Charted p={p} value={d.value as ChartValue} />);

// Charted is the chart of a block; in a widget it takes its six thirds of a line.
function Charted({ p, value }: { p: Shown & { kind?: string }; value: ChartValue }) {
  const fit = useContext(Fit);
  return (
    <Suspense fallback={<Pending />}>
      <div data-chart-block style={fit ? { height: 'calc(6 * var(--third))' } : undefined}>
        <Chart value={value} kind={p.kind} s={p} h={fit ? '100%' : undefined} />
      </div>
    </Suspense>
  );
}
