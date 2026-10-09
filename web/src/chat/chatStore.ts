import { useSyncExternalStore } from 'react';

export interface ChatScreen {
  project?: string;
  screen: string;
  record?: number;
}

// The screen the owner is on goes with each message, so the master knows what "this" is.
let current: ChatScreen = { screen: 'cockpit' };
export const setChatScreen = (s: ChatScreen) => { current = s; };

// chatScreen is the screen of the owner as a message carries it.
export function chatScreen(): string {
  const out: Record<string, unknown> = { screen: current.screen };
  if (current.project) out.project = current.project;
  if (current.record) out[current.screen === 'process' && !current.project ? 'process' : 'record'] = current.record;
  return JSON.stringify(out);
}

// A topic another screen opened waits here until the chat shows it.
let wanted: number | 'new' | undefined;
const opens = new Set<(topic: number | 'new') => void>();
// A new topic opened to dictate into waits here until a composer puts the microphone in reach.
let voice = false;
const voices = new Set<() => void>();
const hear = () => { if (voice && voices.size) { voice = false; voices.forEach((f) => f()); } };
export function onVoiceWanted(f: () => void): () => void {
  voices.add(f);
  setTimeout(hear, 0);
  return () => { voices.delete(f); };
}

// onTopicOpen follows the topics asked for, starting with the one waiting.
export function onTopicOpen(show: (topic: number | 'new') => void): () => void {
  const take = (topic: number | 'new') => { wanted = undefined; show(topic); };
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

// openTopic shows a topic in the chat panel and opens the panel; a new one waits for the owner's voice.
export function openTopic(topic: number | 'new') {
  wanted = topic;
  opens.forEach((f) => f(topic));
  setChatOpen(true);
  voice = topic === 'new';
  setTimeout(hear, 0);
}

// useChatPanel is whether the chat panel is open, and its switch.
export function useChatPanel() {
  const isOpen = useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => open);
  return { open: isOpen, setOpen: setChatOpen };
}
