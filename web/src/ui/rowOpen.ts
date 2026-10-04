import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { Action } from '../lib/catalog.gen';
import { dispatch } from './dispatch';

// rowOpen is what a click on a row does: the move to a screen of its primary button.
export function rowOpen(actions: Action[] | undefined, context: ComponentContext) {
  const act = actions?.find((a) => a.primary && a.screen);
  if (!act) return {};
  return {
    'data-opens': true,
    style: { cursor: 'pointer' },
    onClick: (e: { target: EventTarget }) => {
      if ((e.target as HTMLElement).closest('button, a, input, textarea, [role="button"], [role="combobox"]')) return;
      if (window.getSelection()?.toString()) return;
      void dispatch(context, act);
    },
  };
}
