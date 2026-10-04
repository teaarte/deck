import { useEffect, useRef } from 'react';

// useMountedRef says whether the component is still on the page, for work that ends after it may be gone.
export function useMountedRef() {
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  return alive;
}
