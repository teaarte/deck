import type { RefObject } from 'react';
import { call } from '../../lib/api';
import { events } from '../../lib/events';
import catalogSchema from '../../lib/catalogSchema';

export const FINAL: string[] = catalogSchema.final_states;

export interface ProcessState {
  state: string;
  result?: unknown;
  error?: string;
}

// useProcessWatch follows a started process until it ends: it is read again on each processes event, and every 10 s
// should an event be missed. It stops early once the form is gone.
export function useProcessWatch(alive: RefObject<boolean>) {
  return (process: number, onState: (p: ProcessState) => void) => new Promise<void>((finished, failed) => {
    let over = false;
    const check = async () => {
      try {
        const p = await call<ProcessState>('deck.process_get', { process });
        if (over || !alive.current) return end();
        onState(p);
        if (FINAL.includes(p.state)) end();
      } catch (e) {
        end(e);
      }
    };
    const timer = setInterval(() => void check(), 10000);
    const off = events((e) => { if (e.topic === 'processes') void check(); });
    const end = (e?: unknown) => {
      if (over) return;
      over = true;
      clearInterval(timer);
      off();
      if (e) failed(e); else finished();
    };
    void check();
  });
}
