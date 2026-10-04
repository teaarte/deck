import type { Action } from '../lib/catalog.gen';
import { useNow } from '../hooks/useNow';

// useVisibleActions drops the buttons whose time ran out; only a group with such a time follows the clock.
export function useVisibleActions(actions?: Action[]): Action[] {
  const all = actions ?? [];
  const now = useNow(all.some((a) => a.until));
  return all.filter((a) => !a.until || Date.parse(a.until) > now);
}
