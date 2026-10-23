import { createContext, useContext } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { Action } from '../lib/catalog.gen';
import i18n from '../lib/i18n';
import { errorText } from '../lib/notify';
import { offerUndo } from '../ui/actions/undo';
import { act } from './act';

// Reply is what the handler of a screen says of a press, at /press/<reply> of the data of its surface: why the call
// failed, or its answer.
export interface Reply { error?: string; result?: unknown }

export const PRESS = '/press/';

// BlockContext is the A2UI context of the block a button is drawn in; its presses go out as actions of the surface.
export const BlockContext = createContext<ComponentContext | undefined>(undefined);

export const useBlockContext = () => useContext(BlockContext);

let presses = 0;

// name is the name of the A2UI action of a press: the command a button calls, open for a move, chat for the chat.
const name = (a: Action) => a.command ?? (a.screen ? 'open' : 'chat');

// press sends a button with what the owner gave it as an action of the surface of its block and reads the reply;
// a button outside a screen, such as one of the bell, acts by itself.
export async function press(ctx: ComponentContext | undefined, a: Action, input?: Record<string, unknown>): Promise<Reply> {
  const action = input ? { ...a, params: { ...a.params, ...input } } : a;
  if (!ctx) return run(action);
  const reply = 'r' + ++presses;
  await ctx.dispatchAction({ event: { name: name(action), context: { ...action, reply } } });
  const model = ctx.dataContext.surface.dataModel;
  const got = model.get(PRESS + reply) as Reply | undefined;
  model.set(PRESS + reply, undefined);
  return got ?? { error: i18n.t('press.lost') };
}

// readBlock asks the screen for the data of a block again: by a filter picked, the next page, a dialog opened.
export function readBlock(ctx: ComponentContext, kind: 'filter' | 'more' | 'dialog', context: Record<string, unknown> = {}) {
  void ctx.dispatchAction({ event: { name: kind, context: { block: ctx.componentModel.id, ...context } } });
}

// run acts on a pressed button and offers its undo once it went through; a failed call is its error.
export async function run(a: Action): Promise<Reply> {
  try {
    const result = await act(a);
    if (a.undo) offerUndo(a.undo, a.label || (a.verb ? i18n.t('verb.' + a.verb) : ''));
    return { result };
  } catch (e) {
    return { error: errorText(e) };
  }
}
