import type { ChatValue } from '../lib/catalog.gen';
import { Chat } from '../chat';
import { blockImpl } from './blockImpl';

export const chat = blockImpl('chat', (_, d, context) => <Chat value={d.value as ChatValue} context={context} />);
