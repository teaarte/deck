import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { liveRead } from '../lib/live';

// last is the last answer of each read in this tab, so a read mounted again shows it at once while it reads anew.
const last = new Map<string, unknown>();

// useLiveRead is a read of a command kept fresh by liveRead, read anew in another language; it is its last answer in
// this tab, else initial, until the first answer, and off reads nothing.
export function useLiveRead<T>(command: string, input: Record<string, unknown>, initial: T, off = false): T {
  const lang = useTranslation().i18n.language;
  const key = lang + ' ' + command + JSON.stringify(input);
  const [data, setData] = useState<{ key: string; value: T }>(() => ({ key, value: (last.get(key) as T | undefined) ?? initial }));
  useEffect(() => {
    if (off) return;
    return liveRead<T>(command, input, (value) => {
      last.set(key, value);
      setData({ key, value });
    });
  }, [key, off]);
  if (data.key === key) return data.value;
  return (last.get(key) as T | undefined) ?? data.value;
}
