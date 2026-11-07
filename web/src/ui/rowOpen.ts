import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { Action } from '../lib/catalog.gen';
import { press } from '../screen/press';

// opener is the button a click on a row presses: the move to a screen of its primary button, else of its open button.
export const opener = (actions: Action[] | undefined) =>
  actions?.find((a) => a.primary && a.screen) ?? actions?.find((a) => a.verb === 'open' && a.screen);

// opens says a button is the row's plain opener, its open or a move to the page of its own record, which the row
// itself presses and so stands nowhere as a button; a call to act, as Add the key, stays one.
export const opens = (actions: Action[] | undefined) => {
  const act = opener(actions);
  const params = (act?.params ?? {}) as Record<string, unknown>;
  const plain = !!act && (act.verb === 'open' || !act.verb && act.record != null && !params.dialog && !params.tab);
  return (a: Action) => plain && a === act;
};

// rowOpen is what a click on a row, or Enter on it, does: press its opener in the block of ctx; a line not of a table
// is a link to screen readers.
export function rowOpen(actions: Action[] | undefined, ctx: ComponentContext | undefined, tableRow = false) {
  const act = opener(actions);
  if (!act) return {};
  const inner = (t: EventTarget) => !!(t as HTMLElement).closest('button, a, input, textarea, [role="button"], [role="combobox"]');
  return {
    'data-opens': true,
    tabIndex: 0,
    role: tableRow ? undefined : 'link',
    style: { cursor: 'pointer' },
    onClick: (e: { target: EventTarget; currentTarget: EventTarget }) => {
      if (inner(e.target) && e.target !== e.currentTarget) return;
      if (window.getSelection()?.toString()) return;
      void press(ctx, act);
    },
    onKeyDown: (e: { key: string; target: EventTarget; currentTarget: EventTarget }) => {
      if (e.key === 'Enter' && e.target === e.currentTarget) void press(ctx, act);
    },
  };
}
