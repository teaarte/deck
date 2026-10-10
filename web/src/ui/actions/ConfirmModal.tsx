import { useEffect, useState } from 'react';
import { Alert, Button, Code, Group, List, Loader, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { errorText } from '../../lib/notify';

// DELETE deletes a record with what belongs to it; deck lists that before the owner says yes.
const DELETE = 'deck.record_delete';

type Part = { kind: string; name: string };

// Parts are what deleting a record takes with it, as deck lists them.
function Parts({ record }: { record: unknown }) {
  const { t } = useTranslation();
  const [got, setGot] = useState<{ parts?: Part[]; error?: string }>();
  useEffect(() => {
    let gone = false;
    call<Part[]>('deck.record_parts', { record }).then((parts) => { if (!gone) setGot({ parts }); }, (e) => { if (!gone) setGot({ error: errorText(e) }); });
    return () => { gone = true; };
  }, [record]);
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

// ConfirmModal asks before a call goes: what it does and with which parameters, or what a deletion takes along.
export function ConfirmModal({ opened, label, command, params, onCancel, onConfirm }: {
  opened: boolean; label: string; command?: string; params?: Record<string, unknown>; onCancel: () => void; onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal opened={opened} onClose={onCancel} title={t('confirm.title', { action: label })}>
      <Stack gap="sm" data-confirm>
        {command === DELETE && opened ? <Parts record={params?.record} /> : <Code block>{JSON.stringify(params ?? {}, null, 2)}</Code>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onCancel}>{t('verb.cancel')}</Button>
          <Button color="red" onClick={onConfirm} data-confirm-yes>{label}</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
