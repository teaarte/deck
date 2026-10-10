import { useEffect, useState } from 'react';
import { Alert, Button, Code, Group, List, Loader, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { errorText } from '../../lib/notify';

type Part = { kind: string; name: string };

// Parts are what a call deletes for good, as the read the server named lists them; onReady says whether they came.
function Parts({ from, params, onReady }: { from: string; params?: Record<string, unknown>; onReady: (ok: boolean) => void }) {
  const { t } = useTranslation();
  const [got, setGot] = useState<{ parts?: Part[]; error?: string }>();
  const key = JSON.stringify(params ?? {});
  useEffect(() => {
    let gone = false;
    onReady(false);
    call<Part[]>(from, JSON.parse(key)).then(
      (parts) => { if (!gone) { setGot({ parts }); onReady(true); } },
      (e) => { if (!gone) setGot({ error: errorText(e) }); });
    return () => { gone = true; };
  }, [from, key, onReady]);
  if (got?.error) return <Alert color="red" p="xs">{got.error}</Alert>;
  if (!got?.parts) return <Loader size="xs" />;
  return (
    <Stack gap={4} data-confirm-parts>
      <Text size="sm">{t('confirm.parts')}</Text>
      <List size="sm" spacing={2} style={{ maxHeight: '50vh', overflow: 'auto' }}>
        {got.parts.map((p, i) => <List.Item key={i}>{t('confirm.part.' + p.kind, { defaultValue: p.kind })}: {p.name}</List.Item>)}
      </List>
    </Stack>
  );
}

// ConfirmModal asks before a call goes: what it does and with which parameters, or what it deletes when the server
// names a read of that; then the yes waits for the list.
export function ConfirmModal({ opened, label, parts, params, onCancel, onConfirm }: {
  opened: boolean; label: string; parts?: string; params?: Record<string, unknown>; onCancel: () => void; onConfirm: () => void;
}) {
  const { t } = useTranslation();
  const [ready, setReady] = useState(false);
  useEffect(() => { if (!opened) setReady(false); }, [opened]);
  return (
    <Modal opened={opened} onClose={onCancel} title={t('confirm.title', { action: label })}>
      <Stack gap="sm" data-confirm>
        {parts ? opened && <Parts from={parts} params={params} onReady={setReady} /> : <Code block>{JSON.stringify(params ?? {}, null, 2)}</Code>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onCancel}>{t('verb.cancel')}</Button>
          <Button color="red" onClick={onConfirm} disabled={!!parts && !ready} data-confirm-yes>{label}</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
