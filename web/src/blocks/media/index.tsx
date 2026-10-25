import { Pending } from '../../ui/Pending';
import { lazy, Suspense } from 'react';
import { blockImpl } from '../blockImpl';

const Media = lazy(() => import('./Media').then((m) => ({ default: m.Media })));

// An empty folder of a file manager still shows its steps back.
export const media = blockImpl('media', (_, d) => <Suspense fallback={<Pending />}><Media items={d.files ?? []} crumbs={d.crumbs} browse={!!d.filters?.length} /></Suspense>, {
  empty: (_, d) => (d.crumbs ? <Suspense fallback={<Pending />}><Media items={[]} crumbs={d.crumbs} empty={d.empty} /></Suspense> : undefined),
});
