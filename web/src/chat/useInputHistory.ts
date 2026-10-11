import { useRef } from 'react';
import { useLocalStorage } from '@mantine/hooks';

const MAX = 50;

// useInputHistory keeps the messages sent from this device, newest first; walk goes to an older one (1) or a newer
// one (-1) while the input is empty or shows the one walked to, else it gives nothing and the key moves the caret.
export function useInputHistory() {
  const [list, setList] = useLocalStorage<string[]>({ key: 'deck-chat-history', defaultValue: [] });
  const at = useRef<number | null>(null);
  const walk = (dir: 1 | -1, text: string): string | undefined => {
    if (at.current === null) {
      if (dir < 0 || text !== '' || !list.length) return undefined;
      at.current = 0;
      return list[0];
    }
    if (text !== list[at.current]) {
      at.current = null;
      return undefined;
    }
    const next = at.current + dir;
    if (next < 0) {
      at.current = null;
      return '';
    }
    if (next >= list.length) return undefined;
    at.current = next;
    return list[next];
  };
  const push = (text: string) => {
    at.current = null;
    setList((prev) => [text, ...prev.filter((x) => x !== text)].slice(0, MAX));
  };
  return { walk, push };
}
