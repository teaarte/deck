import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { press, useBlockContext, type Reply } from '../../screen/press';

// usePress presses a button once at a time with what the owner gave it; busy is while its press goes, error why the
// reply says it failed, until the next press or clear.
export function usePress(act: Action) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const running = useRef(false);
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (input?: Record<string, unknown>): Promise<Reply | undefined> => {
    if (running.current) return undefined;
    running.current = true;
    setBusy(true);
    setError('');
    try {
      const r = await press(ctx, act, input);
      setError(r.error ?? '');
      return r;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { busy, label, send, error, clear: () => setError('') };
}
