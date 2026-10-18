import type { Action } from '../lib/catalog.gen';
import type { ScreenActions } from '../screen/ScreenActions';

// opener is the button a click on a row presses: the move to a screen of its primary button, else of its open button.
export const opener = (actions: Action[] | undefined) =>
  actions?.find((a) => a.primary && a.screen) ?? actions?.find((a) => a.verb === 'open' && a.screen);

// rowOpen is what a click on a row does: press its opener.
export function rowOpen(actions: Action[] | undefined, run: ScreenActions['run']) {
  const act = opener(actions);
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
