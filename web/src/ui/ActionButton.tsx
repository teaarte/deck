import { useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Button } from '@mantine/core';
import type { Action } from '../lib/catalog.gen';
import { ActionInput } from './ActionInput';
import { ConfirmModal } from './ConfirmModal';
import { useActionRun, type Before } from './useActionRun';
import { verbIcon } from './verbIcons';

// ActionButton is one button of a screen: it asks for its input or a confirmation first when it has one.
export function ActionButton({ act, context, params, onDone, before, inMenu }: { act: Action; context: ComponentContext; params?: Record<string, unknown>; onDone?: () => void; before?: Before; inMenu?: boolean }) {
  const { busy, label, send } = useActionRun(act, context, { params, onDone, before });
  const [typing, setTyping] = useState(false);
  const [asking, setAsking] = useState(false);
  const variant = inMenu ? 'subtle' : act.primary ? 'filled' : 'default';
  if (act.input && !params) {
    if (typing) return <ActionInput act={act} busy={busy} onCancel={() => setTyping(false)} onSend={(text) => void send({ [act.input!]: text }).then((ok) => { if (ok) setTyping(false); })} />;
    return <Button size="xs" variant={variant} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu} leftSection={verbIcon(act)} data-verb={act.verb} onClick={() => setTyping(true)}>{label}</Button>;
  }
  const button = (
    <Button size="xs" variant={variant} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu}
      color={inMenu && ['delete', 'remove', 'revoke'].includes(act.verb ?? '') ? 'red' : undefined} loading={busy} data-verb={act.verb} leftSection={verbIcon(act)}
      onClick={() => (act.confirm ? setAsking(true) : void send())}>{label}</Button>
  );
  if (!act.confirm) return button;
  return (
    <>
      {button}
      <ConfirmModal opened={asking} label={label} params={act.params} onCancel={() => setAsking(false)} onConfirm={() => { setAsking(false); void send(); }} />
    </>
  );
}
