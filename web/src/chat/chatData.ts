import type { ChatValue, Data } from '../lib/catalog.gen';

// ChatData is the chat as one value: the topic picked with its messages, the topics and the turn, which the server
// sends at paths of their own.
export type ChatData = ChatValue & Required<Pick<ChatValue, 'topics' | 'archived' | 'messages' | 'questions' | 'suggest' | 'asks' | 'send' | 'models'>>;

const NONE: ChatData = { topics: [], archived: [], messages: [], questions: [], suggest: [], asks: [], send: {}, models: [] };

// chatData puts the parts of the chat together; a part not read yet is empty.
export function chatData(...parts: (Data | undefined)[]): ChatData {
  return Object.assign({}, NONE, ...parts.map((p) => (p?.value ?? {}) as ChatValue));
}
