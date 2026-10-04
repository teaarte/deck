import type { BoardValue } from '../../lib/catalog.gen';
import { Board } from '../../board';
import { blockImpl } from '../blockImpl';

export const board = blockImpl('board', (_, d, context) => <Board value={d.value as BoardValue} context={context} />);
