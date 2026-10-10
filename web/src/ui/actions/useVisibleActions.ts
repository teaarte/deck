import type { Action } from '../../lib/catalog.gen';
import { useNow } from '../../hooks/useNow';

// useVisibleActions drops the buttons whose time ran out; only a group with such a time follows the clock.
export function useVisibleActions(actions?: Action[]): Action[] {
  const all = actions ?? [];
  const now = useNow(all.some((a) => a.until));
  return all.filter((a) => !a.until || Date.parse(a.until) > now);
}

// actionKeys are keys of buttons by what they do, so a button keeps its open input when others come or go.
export function actionKeys(actions: Action[]): string[] {
  const seen = new Map<string, number>();
  return actions.map((a) => {
    const k = JSON.stringify([a.verb, a.label, a.command, a.screen, a.project, a.record, a.params]);
    const n = seen.get(k) ?? 0;
    seen.set(k, n + 1);
    return n ? k + '#' + n : k;
  });
}
