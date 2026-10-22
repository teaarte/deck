import { useSyncExternalStore } from 'react';

// Store is one value of the page and those who follow it.
export interface Store<T> {
  get: () => T;
  set: (v: T) => void;
  subscribe: (f: () => void) => () => void;
}

// createStore keeps a value; set replaces it and tells every follower.
export function createStore<T>(initial: T): Store<T> {
  let value = initial;
  const subs = new Set<() => void>();
  return {
    get: () => value,
    set: (v) => { value = v; subs.forEach((f) => f()); },
    subscribe: (f) => { subs.add(f); return () => { subs.delete(f); }; },
  };
}

// useStore is the value of a store, or a part of it by select, drawn again when it changes.
export function useStore<T>(store: Store<T>): T;
export function useStore<T, S>(store: Store<T>, select: (v: T) => S): S;
export function useStore<T>(store: Store<T>, select?: (v: T) => unknown) {
  return useSyncExternalStore(store.subscribe, select ? () => select(store.get()) : store.get);
}
