import { createContext, useContext } from 'react';
import type { Action } from '../lib/catalog.gen';
import type { Route } from '../app/router';
import { act } from './act';

// ScreenActions is what the blocks of a screen do: run a button, which throws when its call fails, and narrow a block
// by a filter, which reads the screen again.
export interface ScreenActions {
  run(a: Action): Promise<boolean>;
  filter(block: string, field: string, value: string): void;
}

export const ScreenActionsContext = createContext<ScreenActions>({ run: act, filter: () => undefined });

export const useScreenActions = () => useContext(ScreenActionsContext);

export const RouteContext = createContext<Route | undefined>(undefined);

// useScreenRoute is the route of the screen a block is drawn on.
export const useScreenRoute = () => useContext(RouteContext);
