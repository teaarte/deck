import { useContext, useLayoutEffect, useRef } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Card, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Shown } from '../lib/format';
import { BlockContext, press } from '../screen/press';
import { NamedIcon } from '../ui/NamedIcon';
import { Pending } from '../ui/Pending';
import { Value } from '../ui/Value';
import b from './blocks.module.css';
import { Fit } from './around';
import { api, type BlockProps } from './blockImpl';
import s from './stat.module.css';

// stat is one number with its icon in the color of its tone and its title under it; a click opens what it counts.
export const stat = createComponentImplementation(api('stat'), ({ props, context }) => {
  const p = props as BlockProps & Shown;
  return <BlockContext.Provider value={context}><Stat p={p} context={context} /></BlockContext.Provider>;
});

// useOneLine makes the text of el smaller until it stands in one line of its width.
function useOneLine(on: boolean, key: unknown) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!on || !el) return;
    const fit = () => {
      el.style.fontSize = '';
      const over = el.scrollWidth / Math.max(1, el.clientWidth);
      if (over > 1) el.style.fontSize = `${Math.max(12, parseFloat(getComputedStyle(el).fontSize) / over)}px`;
    };
    fit();
    const seen = new ResizeObserver(fit);
    seen.observe(el);
    return () => seen.disconnect();
  }, [on, key]);
  return ref;
}

function Stat({ p, context }: { p: BlockProps & Shown; context: ComponentContext }) {
  const { t, i18n } = useTranslation();
  const fit = useContext(Fit);
  const d = p.data;
  const one = useOneLine(fit, `${i18n.language} ${JSON.stringify(d?.value)}`);
  const opens = d?.actions?.find((a) => a.screen);
  const open = () => { if (opens) void press(context, opens); };
  let value;
  if (!d) value = <Pending />;
  else if (d.state === 'error') value = <Text span inherit c="red" title={fit ? d.error : undefined}>{t('state.error')}</Text>;
  else if (d.state === 'empty') value = <Text span inherit c="dimmed" fz="sm" fw={500} data-empty>{d.empty || t('state.empty')}</Text>;
  else value = <Value v={d.value} s={p} tone={d.tone} />;
  return (
    <Card className={`${b.block} ${s.stat}`} data-block data-stat-block data-fit={fit || undefined} data-span={String(p.span ?? 1)} data-tone={d?.tone}
      data-opens={opens ? true : undefined} role={opens ? 'link' : undefined} tabIndex={opens ? 0 : undefined} onClick={open}
      onKeyDown={(e) => { if (opens && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); open(); } }}>
      <span className={s.icon}><NamedIcon name={p.icon} fallback={p.title ?? ''} size={18} /></span>
      <div className={s.body}>
        <Text component="div" className={s.value} data-stat ref={one}>{value}</Text>
        {p.title && <Text className={s.title} truncate>{p.title}</Text>}
        {p.description && !fit && <Text size="xs" c="dimmed" lineClamp={2} data-stat-description>{p.description}</Text>}
        {d?.state === 'error' && !fit && <Text size="xs" c="red" lineClamp={3} data-stat-error>{d.error}</Text>}
      </div>
    </Card>
  );
}
