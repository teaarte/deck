import { createContext, useEffect } from 'react';
import { Portal, UnstyledButton } from '@mantine/core';
import { IconArrowBackUp, IconCheck } from '@tabler/icons-react';
import i18n from '../../lib/i18n';
import { notifyError } from '../../lib/notify';
import { createStore, useStore } from '../../lib/store';
import type { Action } from '../../lib/catalog.gen';
import { act } from '../../screen/act';
import s from './undo.module.css';

const SHOWN = 5000;

interface Offer { id: number; label: string; undo: () => void }

const offer = createStore<Offer | undefined>(undefined);
let offers = 0;
const drop = (id: number) => {
  if (offer.get()?.id === id) offer.set(undefined);
};

// held sends the press that waits now, so a new one does not take away the Undo of one still to go.
let held: (() => void) | undefined;

// holdPress offers for a moment to take back a press before it goes; true once it may go: the moment passed, a new
// press came, or the page is hidden or leaving.
export function holdPress(label: string): Promise<boolean> {
  held?.();
  return new Promise((resolve) => {
    const id = ++offers;
    let timer = 0;
    const finish = (go: boolean) => {
      clearTimeout(timer);
      removeEventListener('pagehide', send);
      document.removeEventListener('visibilitychange', hide);
      if (held === send) held = undefined;
      drop(id);
      resolve(go);
    };
    const send = () => finish(true);
    const hide = () => { if (document.visibilityState === 'hidden') send(); };
    held = send;
    timer = window.setTimeout(send, SHOWN);
    addEventListener('pagehide', send);
    document.addEventListener('visibilitychange', hide);
    offer.set({ id, label: i18n.t('hold.goes', { label }), undo: () => finish(false) });
  });
}

// Holding is what a press that waits tells the part of the screen it was made in: the press waits, or it is back.
export const Holding = createContext<((held: boolean) => void) | undefined>(undefined);

// offerUndo shows for a moment the call that takes back the one that just went through; a new offer takes its place.
export function offerUndo(undo: Action, label: string) {
  const id = ++offers;
  offer.set({ id, label, undo: () => { drop(id); act(undo).catch((e) => notifyError(e, label)); } });
}

// UndoToast is the offer to take back a call, on top in the middle of the screen with the time it has left.
export function UndoToast() {
  const o = useStore(offer);
  useEffect(() => {
    if (!o) return;
    const timer = setTimeout(() => drop(o.id), SHOWN);
    return () => clearTimeout(timer);
  }, [o]);
  if (!o) return null;
  return (
    <Portal>
      <div key={o.id} className={s.toast} role="status" data-undo data-toast>
        <IconCheck size={17} className={s.icon} />
        <span className={s.text}>{o.label}</span>
        <UnstyledButton className={s.undo} onClick={o.undo} data-undo-button>
          <IconArrowBackUp size={15} />{i18n.t('verb.undo')}
        </UnstyledButton>
        <span className={s.bar} style={{ animationDuration: `${SHOWN}ms` }} />
      </div>
    </Portal>
  );
}
