import { useState } from 'react';
import { Button, Code, Group, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { dropAsk, usePressAsks } from './pressStore';
import { usePress } from './usePress';

// Answer is a button of the window that answers the ask, as the server gave it.
function Answer({ id, act, answer, primary, busy, onBusy, onError }: {
  id: number; act: Action; answer: string; primary?: boolean; busy: string; onBusy: (b: string) => void; onError: (e: string) => void;
}) {
  const { t } = useTranslation();
  const { send } = usePress(act);
  const go = async () => {
    onBusy(answer);
    onError('');
    const r = await send();
    onBusy('');
    if (r?.error) onError(r.error);
    else if (r) dropAsk(id);
  };
  return (
    <Button variant={primary ? 'filled' : 'default'} loading={busy === answer} disabled={!!busy} onClick={() => void go()} data-press-answer={answer}>
      {t(primary ? 'press.yes' : 'press.no')}
    </Button>
  );
}

// PressAsks is the window over the screen with a call the run of a press waits to make, what it changes and the whole
// input it goes with: yes makes it, no refuses it, later leaves it in the cockpit.
export function PressAsks() {
  const { t } = useTranslation();
  const asks = usePressAsks();
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const a = asks[0];
  return (
    <Modal opened={!!a} onClose={() => a && dropAsk(a.id)} title={<Text fw={700}>{t('press.title')}</Text>} data-press-ask={a?.id}>
      {a && (
        <Stack gap="sm">
          <Text fw={650}>{a.what}</Text>
          {a.where && <Text size="sm" style={{ overflowWrap: 'anywhere' }} data-press-where>{a.where}</Text>}
          {a.payload !== undefined && a.payload !== null && (
            <Code block mah={240} style={{ overflow: 'auto' }} data-press-input>{JSON.stringify(a.payload, null, 2)}</Code>
          )}
          {a.who && <Text size="xs" c="dimmed">{a.who}</Text>}
          {error && <Text size="sm" c="red" data-press-error>{error}</Text>}
          <Group justify="flex-end" gap="xs">
            <Button variant="subtle" color="gray" onClick={() => dropAsk(a.id)} data-press-answer="later">{t('press.later')}</Button>
            <Answer id={a.id} act={a.deny} answer="deny" busy={busy} onBusy={setBusy} onError={setError} />
            <Answer id={a.id} act={a.allow} answer="allow" primary busy={busy} onBusy={setBusy} onError={setError} />
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
