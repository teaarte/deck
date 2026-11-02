import { useEffect, useState } from 'react';
import { Button, Group, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { useLiveRead } from '../hooks/useLiveRead';
import type { Action } from '../lib/catalog.gen';
import { notifyError } from '../lib/notify';
import { press, useBlockContext } from '../screen/press';
import { pushSupported, reconcilePush, subscribePush, unsubscribePush, type Subscription } from './push';

interface Device { id: number; name: string; remove: Action }

// Pushes turns pushes of deck on and off for this device by the buttons of its block and lists every device that
// gets them.
export function Pushes({ subscribe, unsubscribe }: { subscribe?: Action; unsubscribe?: Action }) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [on, setOn] = useState(false);
  const [busy, setBusy] = useState(false);
  const devices = useLiveRead<Device[]>('deck.push_devices_list', {}, []);
  const { key } = useLiveRead<{ key: string }>('deck.push_key_get', {}, { key: '' }, !pushSupported());
  const device = navigator.userAgent.includes('iPhone') ? 'iPhone' : navigator.platform || 'a browser';
  // tell presses a button with the subscription of this device, and its name when it subscribes; a refusal throws.
  const tell = (act: Action | undefined, named = false) => async (sub: Subscription) => {
    if (!act) return;
    const r = await press(ctx, act, named ? { ...sub, device } : { ...sub });
    if (r.error) throw new Error(r.error);
  };
  useEffect(() => { void reconcilePush(tell(subscribe, true)).then(setOn, () => setOn(false)); }, []);
  const toggle = async () => {
    setBusy(true);
    try {
      if (on) await unsubscribePush(tell(unsubscribe));
      else await subscribePush(key, tell(subscribe, true));
      setOn(!on);
    } catch (e) {
      notifyError(e, t('Pushes on this device'));
    } finally {
      setBusy(false);
    }
  };
  const remove = async (d: Device) => {
    const r = await press(ctx, d.remove);
    if (r.error) notifyError(r.error, t('Pushes on this device'));
  };
  return (
    <Stack gap={4} data-pushes>
      {pushSupported() ? (
        <Group gap="xs">
          <Button size="xs" variant={on ? 'default' : 'filled'} loading={busy} disabled={!key} onClick={() => void toggle()} data-push-toggle>
            {(on ? unsubscribe : subscribe)?.label}
          </Button>
        </Group>
      ) : <Text size="xs" c="dimmed">{t('This browser takes no pushes; on an iPhone, add deck to the home screen first')}</Text>}
      {devices.map((d) => (
        <Group key={d.id} gap="xs" data-push-device>
          <Text size="xs">{d.name}</Text>
          <Button size="compact-xs" variant="subtle" onClick={() => void remove(d)}>{d.remove.label}</Button>
        </Group>
      ))}
    </Stack>
  );
}
