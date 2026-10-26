import { useEffect, useState } from 'react';
import { Button, Code, Group, Paper, Portal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { events } from '../../lib/events';
import { dropAsk, settleAsks, usePressAsks } from './pressStore';
import { usePress } from './usePress';
import s from './PressAsks.module.css';

// Answer is a button of the card that answers the ask, as the server gave it.
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
    if (r?.error) {
      onError(r.error);
      void settleAsks();
    } else if (r) dropAsk(id);
  };
  return (
    <Button size="xs" variant={primary ? 'filled' : 'default'} loading={busy === answer} disabled={!!busy} onClick={() => void go()} data-press-answer={answer}>
      {t(primary ? 'press.yes' : 'press.no')}
    </Button>
  );
}

// PressAsks is the card at the foot of the screen with a call the run of a press waits to make and its whole input: yes
// makes it, no refuses it, later leaves it in the cockpit; the screen under it stays pressable.
export function PressAsks() {
  const { t } = useTranslation();
  const asks = usePressAsks();
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const a = asks[0];
  const shown = !!a;
  useEffect(() => (shown ? events((e) => { if (e.topic === 'asks') void settleAsks(); }) : undefined), [shown]);
  if (!a) return null;
  return (
    <Portal>
      <Paper key={a.id} className={s.card} p="md" shadow="xl" withBorder role="dialog" aria-label={t('press.title')} data-press-ask={a.id} data-toast>
        <Stack gap="xs">
          <Group justify="space-between" wrap="nowrap" gap="xs">
            <Text size="sm" fw={700}>{t('press.title')}</Text>
            {asks.length > 1 && <Text size="xs" c="dimmed">1 / {asks.length}</Text>}
          </Group>
          <Text fw={650}>{a.what}</Text>
          {a.where && <Text size="sm" style={{ overflowWrap: 'anywhere' }} data-press-where>{a.where}</Text>}
          {a.payload !== undefined && a.payload !== null && (
            <Code block mah={160} style={{ overflow: 'auto' }} data-press-input>{JSON.stringify(a.payload, null, 2)}</Code>
          )}
          {a.who && <Text size="xs" c="dimmed">{a.who}</Text>}
          {error && <Text size="sm" c="red" data-press-error>{error}</Text>}
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="subtle" color="gray" onClick={() => dropAsk(a.id)} data-press-answer="later">{t('press.later')}</Button>
            <Answer id={a.id} act={a.deny} answer="deny" busy={busy} onBusy={setBusy} onError={setError} />
            <Answer id={a.id} act={a.allow} answer="allow" primary busy={busy} onBusy={setBusy} onError={setError} />
          </Group>
        </Stack>
      </Paper>
    </Portal>
  );
}
