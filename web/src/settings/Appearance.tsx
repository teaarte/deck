import { useState } from 'react';
import { CheckIcon, ColorSwatch, Group, SegmentedControl, SimpleGrid, Slider, Stack, Switch, Text, useMantineColorScheme, type MantineColorScheme } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { ACCENTS, DENSITIES, FONTS, RADII, SURFACES, THEMES, setLook, useLook, type Look } from '../theme/look';
import { call } from '../lib/api';
import { Pushes } from './Pushes';
import { notifyError } from '../lib/notify';

function Choice<T extends string>({ label, name, value, options, onChange }: {
  label: string; name: string; value: T; options: readonly T[]; onChange: (v: T) => void;
}) {
  const { t } = useTranslation();
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>{label}</Text>
      <SegmentedControl data-look={name} value={value} onChange={(v) => onChange(v as T)} fullWidth size="xs" radius="xl"
        data={options.map((o) => ({ value: o, label: t(`look.${o}`, { defaultValue: o }) }))} />
    </Stack>
  );
}

// Appearance is how deck looks on this device, or with part pushes the pushes of the device.
export function Appearance({ part }: { part?: string }) {
  const { t } = useTranslation();
  const look = useLook();
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  if (part === 'pushes') return <Pushes />;
  return (
    <Stack gap="lg" data-appearance>
      <Text size="sm" c="dimmed">{t('look.device')}</Text>
      <Language />
      <Choice label={t('look.theme')} name="theme" value={look.theme} options={THEMES} onChange={(v: Look['theme']) => setLook(v === 'juce' ? { theme: v, accent: 'coral' } : { theme: v })} />
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="lg" verticalSpacing="lg">
        {look.theme === 'neon' ? (
          <Text size="sm" c="dimmed" data-look-note="scheme">{t('look.dark_only')}</Text>
        ) : (
          <Choice label={t('look.scheme')} name="scheme" value={colorScheme} options={['auto', 'light', 'dark'] as MantineColorScheme[]}
            onChange={setColorScheme} />
        )}
        <Choice label={t('look.surface')} name="surface" value={look.surface} options={SURFACES} onChange={(v: Look['surface']) => setLook({ surface: v })} />
        <Stack gap={6}>
          <Text size="sm" fw={600}>{t('look.accent')}</Text>
          <Group gap={8}>
            {ACCENTS.map((c) => (
              <ColorSwatch key={c} component="button" type="button" color={`var(--mantine-color-${c}-filled)`} aria-label={c} size={26}
                data-accent={c} onClick={() => setLook({ accent: c })} style={{ cursor: 'pointer' }}>
                {look.accent === c && <CheckIcon size={12} color={`var(--mantine-color-${c}-contrast)`} />}
              </ColorSwatch>
            ))}
          </Group>
        </Stack>
        <Stack gap={10}>
          {look.theme === 'juce' ? (
            <Switch label={t('look.sky')} checked={look.sky} onChange={(e) => setLook({ sky: e.currentTarget.checked })} data-look="sky" />
          ) : (
            <Switch label={t('look.pattern')} checked={look.pattern} onChange={(e) => setLook({ pattern: e.currentTarget.checked })} data-look="pattern" />
          )}
          <Switch label={t('look.mascot')} checked={look.mascot} onChange={(e) => setLook({ mascot: e.currentTarget.checked })} data-look="mascot" />
          <Switch label={t('look.motion')} checked={look.motion} onChange={(e) => setLook({ motion: e.currentTarget.checked })} data-look="motion" />
          <Switch label={t('look.slim')} checked={look.slim} onChange={(e) => setLook({ slim: e.currentTarget.checked })} data-look="slim" />
          {look.theme === 'glass' && (
            <Stack gap={4}>
              <Text size="sm" fw={600}>{t('look.tint')}</Text>
              <Slider value={look.tint} min={0} max={1} step={0.05} label={(v) => `${Math.round(v * 100)}%`} data-look="tint"
                marks={[{ value: 0, label: t('look.tint_clear') }, { value: 1, label: t('look.tint_full') }]} mb="md"
                onChange={(v) => setLook({ tint: v })} />
            </Stack>
          )}
        </Stack>
      </SimpleGrid>
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="lg">
        <Choice label={t('look.density')} name="density" value={look.density} options={DENSITIES} onChange={(v: Look['density']) => setLook({ density: v })} />
        <Choice label={t('look.font')} name="font" value={look.font} options={FONTS} onChange={(v: Look['font']) => setLook({ font: v })} />
        {look.theme !== 'pixel' && (
          <Choice label={t('look.radius')} name="radius" value={look.radius} options={RADII} onChange={(v: Look['radius']) => setLook({ radius: v })} />
        )}
      </SimpleGrid>
    </Stack>
  );
}

// Language is the language of the interface for the whole workspace; auto follows each device.
function Language() {
  const { t } = useTranslation();
  const [value, setValue] = useState(() => localStorage.getItem('deck-language') ?? 'auto');
  const pick = async (v: string) => {
    setValue(v);
    try {
      await call('deck.settings_set', { name: 'language', value: v });
    } catch (e) {
      notifyError(e, t('look.language'));
    }
  };
  return (
    <Stack gap={6}>
      <Text size="sm" fw={600}>{t('look.language')}</Text>
      <SegmentedControl data-look="language" value={value} onChange={(v) => void pick(v)} fullWidth size="xs" radius="xl"
        data={[{ value: 'auto', label: t('look.auto') }, { value: 'en', label: 'English' }, { value: 'ru', label: 'Русский' }]} />
    </Stack>
  );
}
