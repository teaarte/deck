import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { download } from '../../lib/api';
import { errorText } from '../../lib/notify';
import type { Action } from '../../lib/catalog.gen';
import { useScreenActions } from '../../screen/ScreenActions';
import { offerUndo } from './undo';

export type Before = () => Promise<void>;

// useActionRun runs a button once at a time: its download or its call, the undo it offers and onDone after it went
// through; send says whether it did, error is why it did not until the next press or clear.
export function useActionRun(act: Action, { params, onDone, before }: { params?: Record<string, unknown>; onDone?: () => void; before?: Before } = {}) {
  const { t } = useTranslation();
  const { run } = useScreenActions();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const running = useRef(false);
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (extra?: Record<string, unknown>): Promise<boolean> => {
    if (running.current) return false;
    running.current = true;
    setBusy(true);
    setError('');
    try {
      await before?.();
      if (act.file) {
        await download(act.file);
        return false;
      }
      const ok = await run({ ...act, params: { ...act.params, ...params, ...extra } });
      if (ok) {
        if (act.undo) offerUndo(act.undo, label, run);
        onDone?.();
      }
      return ok;
    } catch (e) {
      setError(errorText(e));
      return false;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  return { busy, label, send, error, clear: () => setError('') };
}
