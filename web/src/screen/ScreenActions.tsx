import { createContext, useContext } from 'react';
import type { Action } from '../lib/catalog.gen';
import { act } from './act';

// ScreenActions is what the blocks of a screen do: run a button, saying whether it went through, and narrow a block
// by a filter, which reads the screen again.
export interface ScreenActions {
  run(a: Action): Promise<boolean>;
  filter(block: string, field: string, value: string): void;
}

export const ScreenActionsContext = createContext<ScreenActions>({ run: act, filter: () => undefined });

export const useScreenActions = () => useContext(ScreenActionsContext);
