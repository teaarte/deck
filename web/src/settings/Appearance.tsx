import { useState } from 'react';
import { Button, CheckIcon, ColorSwatch, Group, Select, SimpleGrid, Stack, Switch, Text, useMantineColorScheme, type MantineColorScheme } from '@mantine/core';
import { IconSparkles } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { openTopic } from '../chat/chatStore';
import { ACCENTS, DENSITIES, FONTS, setLook, useLook, type Look } from '../theme/look';
import { useWorn, endPreview } from '../theme/worn';
import { useLiveRead } from '../hooks/useLiveRead';
import { call } from '../lib/api';
import { Pushes } from './Pushes';
import { notifyError } from '../lib/notify';

// Choice is one setting of the look picked from a dropdown.
function Choice<T extends string>({ label, name, value, options, labels, onChange }: {
  label: string; name: string; value: T; options: readonly T[]; labels?: Record<string, string>; onChange: (v: T) => void;
}) {
  const { t } = useTranslation();
  return (
    <Select label={label} size="sm" data-look={name} data-value={value} value={value} allowDeselect={false} checkIconPosition="right"
      onChange={(v) => v && onChange(v as T)} comboboxProps={{ withinPortal: true }}
      data={options.map((o) => ({ value: o, label: labels?.[o] ?? t(`look.${o}`, { defaultValue: o }) }))} />
  );
}

// Appearance is the theme of the whole deck and how deck looks on this device, or with part pushes the pushes of the device.
export function Appearance({ part }: { part?: string }) {
  const { t } = useTranslation();
  const look = useLook();
  const worn = useWorn();
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  if (part === 'pushes') return <Pushes />;
  return (
    <Stack gap="lg" data-appearance>
      <Text size="sm" c="dimmed">{t('look.device')}</Text>
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md" verticalSpacing="md">
        <Language />
        <Theme />
        {worn.schemes.length === 1 ? (
          <Text size="sm" c="dimmed" data-look-note="scheme">{t(worn.schemes[0] === 'dark' ? 'look.dark_only' : 'look.light_only', { theme: worn.title })}</Text>
        ) : (
          <Choice label={t('look.scheme')} name="scheme" value={colorScheme} options={['auto', 'light', 'dark'] as MantineColorScheme[]}
            onChange={(v: MantineColorScheme) => { setColorScheme(v); endPreview(); }} />
        )}
        <Choice label={t('look.density')} name="density" value={look.density} options={DENSITIES} onChange={(v: Look['density']) => setLook({ density: v })} />
        <Choice label={t('look.font')} name="font" value={look.font} options={FONTS} onChange={(v: Look['font']) => setLook({ font: v })} />
      </SimpleGrid>
      <Stack gap={6}>
        <Text size="sm" fw={500}>{t('look.accent')}</Text>
        <Group gap={8}>
          {ACCENTS.map((c) => (
            <ColorSwatch key={c} component="button" type="button" color={`var(--mantine-color-${c}-filled)`} aria-label={c} size={26}
              data-accent={c} onClick={() => setLook({ accent: c })} style={{ cursor: 'pointer' }}>
              {look.accent === c && <CheckIcon size={12} color={`var(--mantine-color-${c}-contrast)`} />}
            </ColorSwatch>
          ))}
        </Group>
      </Stack>
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
        <Switch label={t('look.mascot')} checked={look.mascot} onChange={(e) => setLook({ mascot: e.currentTarget.checked })} data-look="mascot" />
        <Switch label={t('look.motion')} checked={look.motion} onChange={(e) => setLook({ motion: e.currentTarget.checked })} data-look="motion" />
      </SimpleGrid>
    </Stack>
  );
}

interface ThemeRow { name: string; title: string; current: boolean; problems?: string[] }

// Theme is the theme of the whole deck, from the shipped ones and the owner's; one with an accent gives it to this device.
function Theme() {
  const { t, i18n } = useTranslation();
  const worn = useWorn();
  const listed = useLiveRead<ThemeRow[]>('deck.themes_list', { lang: i18n.language }, ['documents', 'settings'], []);
  const rows = listed.length ? listed : [{ name: worn.name, title: worn.title, current: true }];
  const current = rows.find((r) => r.current)?.name ?? worn.name;
  const pick = async (name: string) => {
    try {
      const got = await call<{ accent?: string }>('deck.theme', { name });
      await call('deck.theme_use', { name });
      endPreview();
      if (got.accent) setLook({ accent: got.accent });
    } catch (e) {
      notifyError(e, t('look.theme'));
    }
  };
  // ask gives the master a task of drawing in the theme and opens its topic.
  const ask = async (command: string, label: string) => {
    try {
      const out = await call<{ topic?: number }>(command, {});
      if (out.topic) openTopic(out.topic);
    } catch (e) {
      notifyError(e, label);
    }
  };
  return (
    <Stack gap={4}>
      <Choice label={t('look.theme')} name="theme" value={current} options={rows.map((r) => r.name)}
        labels={Object.fromEntries(rows.map((r) => [r.name, r.title]))} onChange={(v) => void pick(v)} />
      <Button variant="subtle" size="compact-sm" leftSection={<IconSparkles size={14} />} onClick={() => void ask('deck.covers_draw', t('look.fill_covers'))}
        data-fill-covers style={{ alignSelf: 'flex-start' }}>{t('look.fill_covers')}</Button>
      <Button variant="subtle" size="compact-sm" leftSection={<IconSparkles size={14} />} onClick={() => void ask('deck.theme_draw', t('look.draw_pictures'))}
        data-draw-pictures style={{ alignSelf: 'flex-start' }}>{t('look.draw_pictures')}</Button>
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
  return <Choice label={t('look.language')} name="language" value={value} options={['auto', 'en', 'ru']}
    labels={{ auto: t('look.auto'), en: 'English', ru: 'Русский' }} onChange={(v) => void pick(v)} />;
}
