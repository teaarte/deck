import { useEffect } from 'react';
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
      <div key={o.id} className={s.toast} role="status" data-undo>
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
