import { useState, type ReactNode } from 'react';
import { ActionIcon, Tooltip, type MantineColor } from '@mantine/core';
import type { Action } from '../lib/catalog.gen';
import { ConfirmModal } from './ConfirmModal';
import { Failed } from './Failed';
import { useActionRun } from './useActionRun';

// ActionIconButton is a button of a screen drawn as an icon alone, its label in the tooltip; it asks first when it
// has a confirmation. A button that takes an input is an ActionButton.
export function ActionIconButton({ act, icon, color = 'gray', variant = 'subtle' }: { act: Action; icon: ReactNode; color?: MantineColor; variant?: string }) {
  const { busy, label, send, error, clear } = useActionRun(act);
  const [asking, setAsking] = useState(false);
  return (
    <>
      <Failed error={error} onClose={clear}>
        <Tooltip label={label}>
          <ActionIcon size={28} radius="xl" color={color} variant={variant} loading={busy} aria-label={label} data-verb={act.verb}
            onClick={(e) => { e.stopPropagation(); if (act.confirm) setAsking(true); else void send(); }}>
            {icon}
          </ActionIcon>
        </Tooltip>
      </Failed>
      {act.confirm && <ConfirmModal opened={asking} label={label} params={act.params} onCancel={() => setAsking(false)} onConfirm={() => { setAsking(false); void send(); }} />}
    </>
  );
}
