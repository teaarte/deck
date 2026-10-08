import { useEffect, useSyncExternalStore } from 'react';
import { Portal, UnstyledButton } from '@mantine/core';
import { IconArrowBackUp, IconCheck } from '@tabler/icons-react';
import i18n from '../lib/i18n';
import { notifyError } from '../lib/notify';
import type { Action } from '../lib/catalog.gen';
import type { ScreenActions } from '../screen/ScreenActions';
import s from './undo.module.css';

const SHOWN = 5000;

interface Offer { id: number; label: string; undo: () => void }

let offer: Offer | undefined;
let offers = 0;
const subs = new Set<() => void>();
const publish = () => subs.forEach((f) => f());
const drop = (id: number) => {
  if (offer?.id !== id) return;
  offer = undefined;
  publish();
};

// offerUndo shows for a moment the call that takes back the one that just went through; a new offer takes its place.
export function offerUndo(undo: Action, label: string, run: ScreenActions['run']) {
  const id = ++offers;
  offer = { id, label, undo: () => { drop(id); run(undo).catch((e) => notifyError(e, label)); } };
  publish();
}

// UndoToast is the offer to take back a call, on top in the middle of the screen with the time it has left.
export function UndoToast() {
  const o = useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => offer);
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
