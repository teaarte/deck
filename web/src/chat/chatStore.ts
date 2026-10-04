import { useSyncExternalStore } from 'react';

export interface ChatScreen {
  project?: string;
  screen: string;
  record?: number;
}

// The screen the owner is on, and the card open on it, go with each message, so the master knows what "this" is.
let current: ChatScreen = { screen: 'cockpit' };
let card: number | undefined;
export const setChatScreen = (s: ChatScreen) => { current = s; card = undefined; };
export const setChatCard = (id?: number) => { card = id; };
export const chatCard = () => card;

// chatScreen is the screen of the owner as a message carries it.
export function chatScreen(): string {
  const out: Record<string, unknown> = { screen: current.screen };
  if (current.project) out.project = current.project;
  if (current.record) out[current.screen === 'process' && !current.project ? 'process' : 'record'] = current.record;
  if (card) out.card = card;
  return JSON.stringify(out);
}

// A topic another screen opened waits here until the chat shows it.
let wanted: number | undefined;
const opens = new Set<(topic: number) => void>();

// onTopicOpen follows the topics asked for, starting with the one waiting.
export function onTopicOpen(show: (topic: number) => void): () => void {
  const take = (topic: number) => { wanted = undefined; show(topic); };
  opens.add(take);
  if (wanted) take(wanted);
  return () => { opens.delete(take); };
}

const OPEN = 'deck-chat-open';
const read = () => {
  try {
    return JSON.parse(localStorage.getItem(OPEN) ?? 'false') === true;
  } catch {
    return false;
  }
};
let open = read();
const subs = new Set<() => void>();
const publish = () => subs.forEach((f) => f());
window.addEventListener('storage', (e) => {
  if (e.key !== OPEN) return;
  open = read();
  publish();
});

// setChatOpen opens or closes the chat panel; the device keeps it.
export function setChatOpen(v: boolean) {
  open = v;
  localStorage.setItem(OPEN, JSON.stringify(v));
  publish();
}

// openTopic shows a topic in the chat panel and opens the panel.
export function openTopic(topic: number) {
  wanted = topic;
  opens.forEach((f) => f(topic));
  setChatOpen(true);
}

// useChatPanel is whether the chat panel is open, and its switch.
export function useChatPanel() {
  const isOpen = useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => open);
  return { open: isOpen, setOpen: setChatOpen };
}
