import { Pending } from '../../ui/Pending';
import { lazy, Suspense, useContext } from 'react';
import { Fit } from '../around';
import type { ChartValue } from '../../lib/catalog.gen';
import type { Shown } from '../../lib/format';
import { blockImpl } from '../blockImpl';

const Chart = lazy(() => import('./Chart').then((m) => ({ default: m.Chart })));

export const chart = blockImpl<Shown & { kind?: string }>('chart', (p, d) => <Charted p={p} value={d.value as ChartValue} />);

// Charted is the chart of a block; in a widget it takes at least three thirds of a line and grows over the room left.
function Charted({ p, value }: { p: Shown & { kind?: string }; value: ChartValue }) {
  const fit = useContext(Fit);
  if (fit) {
    return (
      <div data-chart-block style={{ position: 'relative', flex: 1, minHeight: 'calc(3 * var(--third))' }}>
        <div style={{ position: 'absolute', inset: 0 }}>
          <Suspense fallback={<Pending />}><Chart value={value} kind={p.kind} s={p} h="100%" /></Suspense>
        </div>
      </div>
    );
  }
  return (
    <Suspense fallback={<Pending />}>
      <div data-chart-block><Chart value={value} kind={p.kind} s={p} /></div>
    </Suspense>
  );
}
