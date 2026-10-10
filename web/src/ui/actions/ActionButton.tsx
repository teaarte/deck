import { useState } from 'react';
import { Button } from '@mantine/core';
import type { Action } from '../../lib/catalog.gen';
import { ActionInput } from './ActionInput';
import { ConfirmModal } from './ConfirmModal';
import { Failed } from './Failed';
import { useActionRun, type Before } from './useActionRun';
import { verbIcon } from './verbIcons';

// ActionButton is one button of a screen: it asks for its input or a confirmation first when it has one, and shows
// why a press failed beside it.
export function ActionButton({ act, params, onDone, before, inMenu }: { act: Action; params?: Record<string, unknown>; onDone?: () => void; before?: Before; inMenu?: boolean }) {
  const { busy, label, send, error, clear } = useActionRun(act, { params, onDone, before });
  const [typing, setTyping] = useState(false);
  const [asking, setAsking] = useState(false);
  const variant = inMenu ? 'subtle' : act.primary ? 'filled' : 'default';
  if (act.input && !params) {
    if (typing) {
      return (
        <Failed error={error} onClose={clear}>
          <div><ActionInput act={act} busy={busy} onCancel={() => setTyping(false)} onSend={(text) => void send({ [act.input!]: text }).then((ok) => { if (ok) setTyping(false); })} /></div>
        </Failed>
      );
    }
    return <Button size="xs" variant={variant} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu} leftSection={verbIcon(act)} data-verb={act.verb} onClick={() => setTyping(true)}>{label}</Button>;
  }
  const button = (
    <Failed error={error} onClose={clear}>
      <Button size="xs" variant={variant} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu}
        color={inMenu && ['delete', 'remove', 'revoke'].includes(act.verb ?? '') ? 'red' : undefined} loading={busy} data-verb={act.verb} leftSection={verbIcon(act)}
        onClick={() => (act.confirm ? setAsking(true) : void send())}>{label}</Button>
    </Failed>
  );
  if (!act.confirm) return button;
  return (
    <>
      {button}
      <ConfirmModal opened={asking} label={label} command={act.command} params={act.params} onCancel={() => setAsking(false)} onConfirm={() => { setAsking(false); void send(); }} />
    </>
  );
}
