import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { Action } from '../lib/catalog.gen';

// outcomes holds whether the call behind each pressed button went through, by the ticket the button sent.
export const outcomes = new Map<string, boolean>();
let tickets = 0;

// dispatch sends a button's call or move to the screen and says whether it went through.
export async function dispatch(context: ComponentContext, act: Action): Promise<boolean> {
  const ticket = String(++tickets);
  await context.dispatchAction({ event: { name: act.screen ? 'open' : 'command', context: { ...act, ticket } } });
  const ok = outcomes.get(ticket) !== false;
  outcomes.delete(ticket);
  return ok;
}
