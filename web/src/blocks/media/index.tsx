import { Pending } from '../../ui/Pending';
import { lazy, Suspense } from 'react';
import { blockImpl } from '../blockImpl';

const Media = lazy(() => import('./Media').then((m) => ({ default: m.Media })));

export const media = blockImpl('media', (_, d) => <Suspense fallback={<Pending />}><Media items={d.files ?? []} /></Suspense>);
