import type { RefObject } from 'react';
import { liveRead } from '../../lib/live';
import catalogSchema from '../../lib/catalogSchema';

export const FINAL: string[] = catalogSchema.final_states;

export interface ProcessState {
  state: string;
  result?: unknown;
  error?: string;
}

// useProcessWatch follows a started process until it ends, read live; it stops early once the form is gone.
export function useProcessWatch(alive: RefObject<boolean>) {
  return (process: number, onState: (p: ProcessState) => void) => new Promise<void>((finished, failed) => {
    let over = false;
    let stop = () => undefined as void;
    const end = (e?: unknown) => {
      if (over) return;
      over = true;
      stop();
      if (e) failed(e); else finished();
    };
    stop = liveRead<ProcessState>('deck.process_get', { process }, () => ['processes'], (p) => {
      if (over || !alive.current) return end();
      onState(p);
      if (FINAL.includes(p.state)) end();
    }, end);
  });
}
