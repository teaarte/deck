import { Text } from '@mantine/core';
import type { Shown } from '../lib/format';
import { Value } from '../ui/Value';
import { blockImpl } from './blockImpl';

// stat is one number large, in its format and tone.
export const stat = blockImpl<Shown>('stat', (p, d) => <Text fz={32} fw={700} component="div" data-stat><Value v={d.value} s={p} tone={d.tone} /></Text>);
