import { useEffect, useRef, type DependencyList } from 'react';

// useFocusHandoff lets a button that hides itself hand the focus to the one that brings its part back: the focus
// moves to the selector given once deps changed.
export function useFocusHandoff(deps: DependencyList) {
  const to = useRef('');
  useEffect(() => {
    if (!to.current) return;
    document.querySelector<HTMLElement>(to.current)?.focus();
    to.current = '';
  }, deps);
  return (selector: string) => { to.current = selector; };
}
