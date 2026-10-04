import type { ActionsValue } from '../lib/catalog.gen';
import { Actions } from '../ui/Actions';
import { blockImpl } from './blockImpl';

// actions is a group of buttons on its own.
export const actions = blockImpl('actions', (_, d, context) => <Actions actions={d.value as ActionsValue | undefined} context={context} />);
