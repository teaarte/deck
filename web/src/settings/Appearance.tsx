import { useState } from 'react';
import type { Action } from '../lib/catalog.gen';
import { Button, CheckIcon, ColorSwatch, Group, Select, SimpleGrid, Stack, Switch, Text, useMantineColorScheme, type MantineColorScheme } from '@mantine/core';
import { IconSparkles } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { ACCENTS, DENSITIES, FONTS, setLook, useLook, type Look } from '../theme/look';
import { useWorn, endPreview } from '../theme/worn';
import { useLiveRead } from '../hooks/useLiveRead';
import { press, useBlockContext } from '../screen/press';
import { Pushes } from './Pushes';
import { notifyError } from '../lib/notify';

// OPTIONS are the words of the values of the look.
const OPTIONS: Record<string, string> = {
  auto: 'As the system', light: 'Light', dark: 'Dark', compact: 'Compact', default: 'Normal', comfortable: 'Comfortable',
  small: 'Small', large: 'Large', xs: 'XS', sm: 'S', md: 'M', lg: 'L', xl: 'XL',
};

// Choice is one setting of the look picked from a dropdown.
function Choice<T extends string>({ label, name, value, options, labels, onChange }: {
  label: string; name: string; value: T; options: readonly T[]; labels?: Record<string, string>; onChange: (v: T) => void;
}) {
  const { t } = useTranslation();
  return (
    <Select label={label} size="sm" data-look={name} data-value={value} value={value} allowDeselect={false} checkIconPosition="right"
      onChange={(v) => v && onChange(v as T)} comboboxProps={{ withinPortal: true }}
      data={options.map((o) => ({ value: o, label: labels?.[o] ?? t(OPTIONS[o] ?? o) }))} />
  );
}

// Appearance is the theme of the whole deck and how deck looks on this device, or with part pushes the pushes of the device;
// its buttons are those of its block.
export function Appearance({ part, actions }: { part?: string; actions: Action[] }) {
  const { t } = useTranslation();
  const look = useLook();
  const worn = useWorn();
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  const of = (command: string) => actions.find((a) => a.command === command);
  if (part === 'pushes') return <Pushes subscribe={of('deck.push_subscribe')} unsubscribe={of('deck.push_unsubscribe')} />;
  return (
    <Stack gap="lg" data-appearance>
      <Text size="sm" c="dimmed">{t("The theme is the whole deck's; the rest stays on this device.")}</Text>
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md" verticalSpacing="md">
        <Language act={of('deck.setting_set')} />
        <Theme draws={[of('deck.cover_draw'), of('deck.theme_draw')]} />
        {worn.schemes.length === 1 ? (
          <Text size="sm" c="dimmed" data-look-note="scheme">{t(worn.schemes[0] === 'dark' ? '{{theme}} is dark only.' : '{{theme}} is light only.', { theme: worn.title })}</Text>
        ) : (
          <Choice label={t('Color scheme')} name="scheme" value={colorScheme} options={['auto', 'light', 'dark'] as MantineColorScheme[]}
            onChange={(v: MantineColorScheme) => { setColorScheme(v); endPreview(); }} />
        )}
        <Choice label={t('Density')} name="density" value={look.density} options={DENSITIES} onChange={(v: Look['density']) => setLook({ density: v })} />
        <Choice label={t('Font size')} name="font" value={look.font} options={FONTS} onChange={(v: Look['font']) => setLook({ font: v })} />
      </SimpleGrid>
      <Stack gap={6}>
        <Text size="sm" fw={500}>{t('Accent')}</Text>
        <Group gap={8}>
          {ACCENTS.map((c) => (
            <ColorSwatch key={c} component="button" type="button" color={`var(--mantine-color-${c}-filled)`} aria-label={c} size={26}
              data-accent={c} onClick={() => setLook({ accent: c })} className="deck-pointer">
              {look.accent === c && <CheckIcon size={12} color={`var(--mantine-color-${c}-contrast)`} />}
            </ColorSwatch>
          ))}
        </Group>
      </Stack>
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
        <Switch label={t('Mascot Deki in the chat and on the cockpit')} checked={look.mascot} onChange={(e) => setLook({ mascot: e.currentTarget.checked })} data-look="mascot" />
        <Switch label={t('Animations')} checked={look.motion} onChange={(e) => setLook({ motion: e.currentTarget.checked })} data-look="motion" />
      </SimpleGrid>
    </Stack>
  );
}

interface ThemeRow { name: string; title: string; current: boolean; problems?: string[]; use?: Action }

// Theme is the theme of the whole deck, from the shipped ones and the owner's; one with an accent gives it to this device.
// draws are the buttons that ask the master to draw in it.
function Theme({ draws }: { draws: (Action | undefined)[] }) {
  const { t, i18n } = useTranslation();
  const ctx = useBlockContext();
  const worn = useWorn();
  const listed = useLiveRead<ThemeRow[]>('deck.themes_list', { lang: i18n.language }, []);
  const rows = listed.length ? listed : [{ name: worn.name, title: worn.title, current: true }];
  const current = rows.find((r) => r.current)?.name ?? worn.name;
  const pick = async (name: string) => {
    const use = rows.find((r) => r.name === name)?.use;
    if (!use) return;
    const r = await press(ctx, use);
    if (r.error) return notifyError(r.error, t('Theme'));
    endPreview();
    const accent = (r.result as { accent?: string } | undefined)?.accent;
    if (accent) setLook({ accent });
  };
  const ask = async (act: Action) => {
    const r = await press(ctx, act);
    if (r.error) notifyError(r.error, act.label);
  };
  return (
    <Stack gap={4}>
      <Choice label={t('Theme')} name="theme" value={current} options={rows.map((r) => r.name)}
        labels={Object.fromEntries(rows.map((r) => [r.name, r.title]))} onChange={(v) => void pick(v)} />
      {draws.map((a, i) => a && (
        <Button key={a.command} variant="subtle" size="compact-sm" leftSection={<IconSparkles size={14} />} onClick={() => void ask(a)}
          {...{ [i ? 'data-draw-pictures' : 'data-fill-covers']: true }} className="deck-start">{a.label}</Button>
      ))}
    </Stack>
  );
}

// Language is the language of the interface for the whole workspace; auto follows each device.
function Language({ act }: { act?: Action }) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [value, setValue] = useState(() => localStorage.getItem('deck-language') ?? 'auto');
  const label = act?.label ?? t('Language of the interface (for the whole workspace)');
  const pick = async (v: string) => {
    setValue(v);
    if (!act?.input) return;
    const r = await press(ctx, act, { [act.input]: v });
    if (r.error) notifyError(r.error, label);
  };
  return <Choice label={label} name="language" value={value} options={['auto', 'en', 'ru']}
    labels={{ auto: t('As the system'), en: 'English', ru: 'Русский' }} onChange={(v) => void pick(v)} />;
}
