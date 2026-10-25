import type { Data } from '../lib/catalog.gen';
import { Chat } from '../chat/Chat';
import { chatData } from '../chat/chatData';
import { blockImpl } from './blockImpl';

export const chat = blockImpl<{ topics?: Data; turn?: Data }>('chat', (p, d, context) => (
  <Chat value={chatData(p.topics, d, p.turn)} context={context} />
));
