import { useEffect, useState } from 'react';
import { Button, Group, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../lib/api';
import { notifyError } from '../lib/notify';
import { pushSupported, reconcilePush, subscribePush, unsubscribePush } from './push';

interface Device { id: number; name: string }

// Pushes turns pushes of deck on and off for this device and lists every device that gets them.
export function Pushes() {
  const { t } = useTranslation();
  const [on, setOn] = useState(false);
  const [busy, setBusy] = useState(false);
  const [devices, setDevices] = useState<Device[]>([]);
  const device = navigator.userAgent.includes('iPhone') ? 'iPhone' : navigator.platform || 'a browser';
  const load = async () => {
    setOn(await reconcilePush(device).catch(() => false));
    setDevices(await call<Device[]>('deck.push_devices').catch(() => []));
  };
  useEffect(() => { void load(); }, []);
  const toggle = async () => {
    setBusy(true);
    try {
      if (on) await unsubscribePush();
      else await subscribePush(device);
      await load();
    } catch (e) {
      notifyError(e, t('push.title'));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Stack gap={4} data-pushes>
      {pushSupported() ? (
        <Group gap="xs">
          <Button size="xs" variant={on ? 'default' : 'filled'} loading={busy} onClick={() => void toggle()} data-push-toggle>
            {on ? t('push.off') : t('push.on')}
          </Button>
        </Group>
      ) : <Text size="xs" c="dimmed">{t('push.unsupported')}</Text>}
      {devices.map((d) => (
        <Group key={d.id} gap="xs" data-push-device>
          <Text size="xs">{d.name}</Text>
          <Button size="compact-xs" variant="subtle" onClick={() => void call('deck.push_unsubscribe', { device: d.id }).then(load, (e: Error) => notifyError(e, t('push.title')))}>{t('verb.remove')}</Button>
        </Group>
      ))}
    </Stack>
  );
}
