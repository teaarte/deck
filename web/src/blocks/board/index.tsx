import type { BoardValue } from '../../lib/catalog.gen';
import { Board } from './Board';
import { blockImpl } from '../blockImpl';

export const board = blockImpl('board', (_, d) => <Board value={d.value as BoardValue} />);
