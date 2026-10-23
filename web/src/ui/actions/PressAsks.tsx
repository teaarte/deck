import { useState } from 'react';
import { Button, Code, Group, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { errorText } from '../../lib/notify';
import { dropAsk, usePressAsks } from './pressStore';

// PressAsks is the window over the screen with a call the run of a press waits to make, what it changes and the whole
// input it goes with: yes makes it, no refuses it, later leaves it in the cockpit.
export function PressAsks() {
  const { t } = useTranslation();
  const asks = usePressAsks();
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const a = asks[0];
  const answer = (answer: string) => {
    setBusy(answer);
    setError('');
    call('deck.ask_answer', { ask: a.id, answer }).then(() => dropAsk(a.id), (e) => setError(errorText(e))).finally(() => setBusy(''));
  };
  return (
    <Modal opened={!!a} onClose={() => a && dropAsk(a.id)} title={<Text fw={700}>{t('press.title')}</Text>} radius="lg" data-press-ask={a?.id}>
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
            <Button variant="default" loading={busy === 'deny'} disabled={!!busy} onClick={() => answer('deny')} data-press-answer="deny">{t('press.no')}</Button>
            <Button loading={busy === 'allow'} disabled={!!busy} onClick={() => answer('allow')} data-press-answer="allow">{t('press.yes')}</Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
