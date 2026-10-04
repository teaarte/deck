import { lazy, Suspense } from 'react';
import { Loader } from '@mantine/core';
import { blockImpl } from '../blockImpl';

const Media = lazy(() => import('./Media').then((m) => ({ default: m.Media })));

export const media = blockImpl('media', (_, d) => <Suspense fallback={<Loader size="xs" />}><Media items={d.files ?? []} /></Suspense>);
