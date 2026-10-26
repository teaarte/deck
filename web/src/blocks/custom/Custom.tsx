import { useEffect, useState } from 'react';
import { ActionIcon, Button, Code, Group, Modal, Stack, Text } from '@mantine/core';
import { IconX } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { CustomValue } from '../../lib/catalog.gen';
import { useBlockContext } from '../../screen/press';
import { useAppBridge, type Ask } from './useAppBridge';

// Custom hosts an MCP App written by a model: its HTML runs in a frame of the second origin without network,
// gets the data of its source as a tool result and may call only the commands of its buttons.
export function Custom({ value }: { value: CustomValue }) {
  const { t } = useTranslation();
  const [asking, setAsking] = useState<Ask & { done: (ok: boolean) => void }>();
  const ask = (q: Ask) => new Promise<boolean>((done) => setAsking({ ...q, done }));
  const answer = (ok: boolean) => { asking?.done(ok); setAsking(undefined); };
  const { frame, height, full, inline, failed } = useAppBridge(value, ask, useBlockContext());
  // Allow waits a moment, so a click meant for the screen under a window the frame just opened does not land on it.
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    setArmed(false);
    if (!asking) return;
    const id = setTimeout(() => setArmed(true), 500);
    return () => clearTimeout(id);
  }, [asking]);
  return (
    <>
      {failed && <Text size="sm" c="red" data-custom-error>{t('custom.failed', { why: failed })}</Text>}
      <iframe ref={frame} title="custom" data-custom data-full={full || undefined} sandbox="allow-scripts allow-same-origin allow-forms"
        style={full ? { position: 'fixed', inset: 0, width: '100vw', height: '100dvh', border: 0, zIndex: 190, background: 'var(--mantine-color-body)' }
          : { width: '100%', height, border: 0, display: 'block' }} />
      {full && (
        <ActionIcon variant="default" size="lg" onClick={inline} aria-label={t('custom.inline')} data-custom-inline
          style={{ position: 'fixed', top: 12, right: 12, zIndex: 191 }}><IconX size={18} /></ActionIcon>
      )}
      <Modal opened={!!asking} onClose={() => answer(false)} title={t('custom.ask')}>
        {asking && (
          <Stack gap="sm" data-custom-ask>
            <Text size="sm">{asking.act.label} ({asking.act.command})</Text>
            <Code block>{JSON.stringify(asking.input, null, 2)}</Code>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => answer(false)}>{t('verb.deny')}</Button>
              <Button onClick={() => answer(true)} disabled={!armed} data-custom-allow>{t('verb.allow')}</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </>
  );
}
