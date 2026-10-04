import { Button, Code, Group, Modal, Stack } from '@mantine/core';
import { useTranslation } from 'react-i18next';

// ConfirmModal asks before a call goes: what it does and with which parameters.
export function ConfirmModal({ opened, label, params, onCancel, onConfirm }: {
  opened: boolean; label: string; params?: Record<string, unknown>; onCancel: () => void; onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal opened={opened} onClose={onCancel} title={t('confirm.title', { action: label })}>
      <Stack gap="sm" data-confirm>
        <Code block>{JSON.stringify(params ?? {}, null, 2)}</Code>
        <Group justify="flex-end">
          <Button variant="default" onClick={onCancel}>{t('verb.cancel')}</Button>
          <Button color="red" onClick={onConfirm} data-confirm-yes>{label}</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
