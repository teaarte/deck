import { useEffect, useState } from 'react';

// useTicking is the time now, drawn again every second while on is true.
export function useTicking(on: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!on) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [on]);
  return now;
}
