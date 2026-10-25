import { useContext, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { press, useBlockContext, type Reply } from '../../screen/press';
import { Holding } from './undo';

// usePress presses a button once at a time with what the owner gave it; busy is while its press goes, error why the
// reply says it failed, until the next press or clear. A press that holds tells its part of the screen while it waits.
export function usePress(act: Action) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const running = useRef(false);
  const holding = useContext(Holding);
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (input?: Record<string, unknown>): Promise<Reply | undefined> => {
    if (running.current) return undefined;
    running.current = true;
    setBusy(true);
    setError('');
    if (act.hold) holding?.(true);
    try {
      const r = await press(ctx, act, input);
      setError(r.error ?? '');
      if (act.hold && (r.taken || r.error)) holding?.(false);
      return r;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { busy, label, send, error, clear: () => setError('') };
}
