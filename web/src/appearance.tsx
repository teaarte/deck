import { useEffect, useState } from 'react';
import { Button, CheckIcon, ColorSwatch, Group, SegmentedControl, Stack, Text, useMantineColorScheme, type MantineColorScheme } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { ACCENTS, DENSITIES, FONTS, RADII, setLook, useLook, type Look } from './look';
import { call } from './api';
import { pushSupported, reconcilePush, subscribePush, unsubscribePush } from './push';

function Choice<T extends string>({ label, name, value, options, onChange }: {
  label: string; name: string; value: T; options: readonly T[]; onChange: (v: T) => void;
}) {
  const { t } = useTranslation();
  return (
    <Stack gap={4}>
      <Text size="sm" fw={500}>{label}</Text>
      <SegmentedControl data-look={name} value={value} onChange={(v) => onChange(v as T)}
        data={options.map((o) => ({ value: o, label: t(`look.${o}`, { defaultValue: o }) }))} />
    </Stack>
  );
}

// Appearance is how deck looks on this device: color scheme, accent, density, font size and corners.
export function Appearance() {
  const { t } = useTranslation();
  const look = useLook();
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  return (
    <Stack gap="md" data-appearance>
      <Text size="sm" c="dimmed">{t('look.device')}</Text>
      <Choice label={t('look.scheme')} name="scheme" value={colorScheme} options={['auto', 'light', 'dark'] as MantineColorScheme[]}
        onChange={setColorScheme} />
      <Stack gap={4}>
        <Text size="sm" fw={500}>{t('look.accent')}</Text>
        <Group gap="xs">
          {ACCENTS.map((c) => (
            <ColorSwatch key={c} component="button" type="button" color={`var(--mantine-color-${c}-filled)`} aria-label={c}
              data-accent={c} onClick={() => setLook({ accent: c })} style={{ cursor: 'pointer' }}>
              {look.accent === c && <CheckIcon size={12} color="white" />}
            </ColorSwatch>
          ))}
        </Group>
      </Stack>
      <Choice label={t('look.density')} name="density" value={look.density} options={DENSITIES} onChange={(v: Look['density']) => setLook({ density: v })} />
      <Choice label={t('look.font')} name="font" value={look.font} options={FONTS} onChange={(v: Look['font']) => setLook({ font: v })} />
      <Choice label={t('look.radius')} name="radius" value={look.radius} options={RADII} onChange={(v: Look['radius']) => setLook({ radius: v })} />
      <Pushes />
    </Stack>
  );
}

interface Device { id: number; name: string }

// Pushes turns pushes of deck on and off for this device and lists every device that gets them.
function Pushes() {
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
      notifications.show({ title: t('push.title'), message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Stack gap={4} data-pushes>
      <Text size="sm" fw={500}>{t('push.title')}</Text>
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
          <Button size="compact-xs" variant="subtle" onClick={() => void call('deck.push_unsubscribe', { device: d.id }).then(load)}>{t('verb.remove')}</Button>
        </Group>
      ))}
    </Stack>
  );
}
