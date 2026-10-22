import { useEffect, useState } from 'react';
import { liveRead } from '../lib/live';

// useLiveRead is a read of a command kept fresh by liveRead; it is initial until the first answer, and off reads
// nothing.
export function useLiveRead<T>(command: string, input: Record<string, unknown>, topics: string[] | ((data: T | undefined) => string[]),
  initial: T, off = false): T {
  const [data, setData] = useState<T>(initial);
  const key = JSON.stringify(input);
  useEffect(() => {
    if (off) return;
    const follows = typeof topics === 'function' ? topics : () => topics;
    return liveRead<T>(command, input, follows, setData);
  }, [command, key, off]);
  return data;
}
