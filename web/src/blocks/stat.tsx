import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Card, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Shown } from '../lib/format';
import { useScreenActions } from '../screen/ScreenActions';
import { NamedIcon } from '../ui/NamedIcon';
import { Pending } from '../ui/Pending';
import { Value } from '../ui/Value';
import b from './blocks.module.css';
import { api, type BlockProps } from './blockImpl';
import s from './stat.module.css';

// stat is one number with its icon in the color of its tone and its title under it; a click opens what it counts.
export const stat = createComponentImplementation(api('stat'), ({ props }) => {
  const p = props as BlockProps & Shown;
  return <Stat p={p} />;
});

function Stat({ p }: { p: BlockProps & Shown }) {
  const { t } = useTranslation();
  const d = p.data;
  const { run } = useScreenActions();
  const opens = d?.actions?.find((a) => a.screen);
  const open = () => { if (opens) void run(opens); };
  let value;
  if (!d) value = <Pending />;
  else if (d.state === 'error') value = <Text span inherit c="red">{t('state.error')}</Text>;
  else if (d.state === 'empty') value = <Text span inherit c="dimmed" fz="sm" fw={500} data-empty>{d.empty || t('state.empty')}</Text>;
  else value = <Value v={d.value} s={p} tone={d.tone} />;
  return (
    <Card className={`${b.block} ${s.stat}`} data-block data-stat-block data-span={String(p.span ?? 1)} data-tone={d?.tone}
      data-opens={opens ? true : undefined} role={opens ? 'link' : undefined} tabIndex={opens ? 0 : undefined} onClick={open}
      onKeyDown={(e) => { if (opens && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); open(); } }}>
      <span className={s.icon}><NamedIcon name={p.icon} fallback={p.title ?? ''} size={18} /></span>
      <div className={s.body}>
        <Text component="div" className={s.value} data-stat>{value}</Text>
        {p.title && <Text className={s.title} truncate>{p.title}</Text>}
        {p.description && <Text size="xs" c="dimmed" lineClamp={2} data-stat-description>{p.description}</Text>}
        {d?.state === 'error' && <Text size="xs" c="red" lineClamp={3} data-stat-error>{d.error}</Text>}
      </div>
    </Card>
  );
}
