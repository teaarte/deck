import { useContext, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { press, useBlockContext, type Reply } from '../../screen/press';
import i18n from '../../lib/i18n';
import { notifyError, notifyNote } from '../../lib/notify';
import { outcome } from '../../screen/act';
import { watchRun } from './pressStore';
import { Holding } from './undo';

// told tells the owner what the run a press started came to, while the button stays busy: a run already going, its
// end or failure, or that it goes on; the calls it waits to make come up by themselves.
async function told(result: unknown, label: string) {
  const o = outcome(result);
  if (o.running) {
    notifyNote(i18n.t('press.already', { label }));
    return;
  }
  if (!o.started) return;
  const end = await watchRun(o.started);
  if (!end || end.asks.length) return;
  if (end.state === 'error') notifyError(end.error || i18n.t('press.failed'), label);
  else if (end.state === 'done') notifyNote(i18n.t('press.done', { label }));
  else notifyNote(i18n.t('press.going', { label }));
}

// usePress presses a button once at a time with what the owner gave it; busy is while its press and the run it started
// go, error why the reply says it failed. A press that holds tells its part of the screen while it waits.
export function usePress(act: Action) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const running = useRef(false);
  const holding = useContext(Holding);
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (input?: Record<string, unknown>): Promise<Reply | undefined> => {
    if (running.current) {
      notifyNote(i18n.t('press.already', { label }));
      return undefined;
    }
    running.current = true;
    setBusy(true);
    setError('');
    if (act.hold) holding?.(true);
    try {
      const r = await press(ctx, act, input);
      setError(r.error ?? '');
      if (act.hold && (r.taken || r.error)) holding?.(false);
      if (act.hold && r.error) notifyError(r.error, label);
      if (!r.error) await told(r.result, label);
      return r;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { busy, label, send, error, clear: () => setError('') };
}
