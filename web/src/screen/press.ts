import { createContext, useContext } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { newKey, Refusal } from '../lib/api';
import type { Action } from '../lib/catalog.gen';
import i18n from '../lib/i18n';
import { errorText } from '../lib/notify';
import { holdPress, offerUndo } from '../ui/actions/undo';
import { act } from './act';

// Reply is what a screen says of a press at /press/<reply>: why the call failed, or its answer; gone, that what it acted
// on was over already, so a button tells nothing; lost, that no answer came, so a press again keeps its key.
export interface Reply { error?: string; result?: unknown; taken?: boolean; gone?: boolean; lost?: boolean }

export const PRESS = '/press/';

// BlockContext is the A2UI context of the block a button is drawn in; its presses go out as actions of the surface.
export const BlockContext = createContext<ComponentContext | undefined>(undefined);

export const useBlockContext = () => useContext(BlockContext);

let presses = 0;

// name is the name of the A2UI action of a press: the command a button calls, open for a move, chat for the chat.
const name = (a: Action) => a.command ?? (a.screen ? 'open' : 'chat');

// press sends a button with what the owner gave it and the key of the press as an action of the surface of its block
// and reads the reply; a button outside a screen acts by itself. One that holds waits while the owner may take it back.
export async function press(ctx: ComponentContext | undefined, a: Action, input?: Record<string, unknown>, key = newKey()): Promise<Reply> {
  const action = input ? { ...a, params: { ...a.params, ...input } } : a;
  if (a.hold && !(await holdPress(a.label ?? ''))) return { taken: true };
  if (!ctx) return run(action, key);
  const reply = 'r' + ++presses;
  await ctx.dispatchAction({ event: { name: name(action), context: { ...action, reply, key } } });
  const model = ctx.dataContext.surface.dataModel;
  const got = model.get(PRESS + reply) as Reply | undefined;
  model.set(PRESS + reply, undefined);
  return got ?? { error: i18n.t('deck did not answer the press; look at what it changed before pressing again') };
}

// readBlock asks the screen for the data of a block again: by a filter picked, the next page, a dialog opened, a tab
// shown.
export function readBlock(ctx: ComponentContext, kind: 'filter' | 'more' | 'dialog' | 'tab' | 'unfold', context: Record<string, unknown> = {}) {
  void ctx.dispatchAction({ event: { name: kind, context: { block: ctx.componentModel.id, ...context } } });
}

// run acts on a pressed button with the key of its press and offers its undo once it went through; a failed call is its
// error.
export async function run(a: Action, key?: string): Promise<Reply> {
  try {
    const result = await act(a, key);
    if (a.undo) offerUndo(a.undo, a.label ?? '');
    return { result };
  } catch (e) {
    // A proxy in front of deck that lost it answers 502 to 504: no answer of deck came.
    const lost = !(e instanceof Refusal) || e.status >= 502 && e.status <= 504;
    return { error: errorText(e), gone: e instanceof Refusal && e.status === 410, lost };
  }
}
