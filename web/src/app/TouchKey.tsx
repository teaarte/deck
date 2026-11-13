import { useState } from 'react';
import { Alert, Button, Group, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { errorText } from '../lib/notify';
import { login, useTouch } from '../lib/signin';

// TouchKey asks the owner to touch their passkey for a call that changes what deck trusts for long; the touch signs this
// browser in again, and the call goes.
export function TouchKey() {
  const { t } = useTranslation();
  const touch = useTouch();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const close = () => {
    setError('');
    touch?.drop();
  };
  const go = async () => {
    setBusy(true);
    setError('');
    try {
      await login();
      touch?.done();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened={touch !== null} onClose={close} title={t('Confirm with your passkey')} centered data-touch-key>
      <Stack gap="sm">
        <Text size="sm">{touch?.why}</Text>
        <Text size="xs" c="dimmed">{t('One touch lets such answers through for five minutes.')}</Text>
        {error && <Alert color="red" p="xs" data-touch-error>{error}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={close} disabled={busy}>{t('Cancel')}</Button>
          <Button onClick={() => void go()} loading={busy} data-touch="passkey">{t('Confirm with your passkey')}</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
