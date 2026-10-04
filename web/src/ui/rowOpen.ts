import type { Action } from '../lib/catalog.gen';
import type { ScreenActions } from '../screen/ScreenActions';

// rowOpen is what a click on a row does: the move to a screen of its primary button.
export function rowOpen(actions: Action[] | undefined, run: ScreenActions['run']) {
  const act = actions?.find((a) => a.primary && a.screen);
  if (!act) return {};
  return {
    'data-opens': true,
    style: { cursor: 'pointer' },
    onClick: (e: { target: EventTarget }) => {
      if ((e.target as HTMLElement).closest('button, a, input, textarea, [role="button"], [role="combobox"]')) return;
      if (window.getSelection()?.toString()) return;
      void run(act);
    },
  };
}
