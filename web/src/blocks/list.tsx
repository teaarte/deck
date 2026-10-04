import { Group, Stack, Text } from '@mantine/core';
import s from './list.module.css';
import { show, toneColor } from '../lib/format';
import { Actions } from '../ui/Actions';
import { Details } from '../ui/Details';
import { Markdown } from '../ui/Markdown';
import { NamedIcon } from '../ui/NamedIcon';
import { QuestionPicker } from '../ui/QuestionPicker';
import { rowOpen } from '../ui/rowOpen';
import type { Data } from '../lib/catalog.gen';
import { useScreenActions } from '../screen/ScreenActions';
import { blockImpl } from './blockImpl';

// list draws items: a title with its icon, text, meta, details, the questions of an agent and buttons.
export const list = blockImpl('list', (_, d) => <Items items={d.items ?? []} />);

function Items({ items }: { items: NonNullable<Data['items']> }) {
  const { run } = useScreenActions();
  return (
    <Stack gap="sm">
      {items.map((it, i) => {
        const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
        const rest = (it.actions ?? []).filter((a) => a !== answer);
        const opens = rowOpen(it.actions, run);
        return (
          <Stack key={show(it.id) || i} gap={6} data-item data-tone={it.tone} {...opens}
            style={{ ...opens.style, ...(it.tone && it.tone !== 'muted' ? { borderLeft: `3px solid var(--mantine-color-${toneColor(it.tone)}-6)`, paddingLeft: 'var(--mantine-spacing-xs)' } : {}) }}
            opacity={it.tone === 'muted' ? 0.6 : undefined}>
            <Group gap={10} wrap="nowrap" align="flex-start">
              {it.icon && <span className={s.itemIcon}><NamedIcon name={it.icon} fallback={it.title} size={18} /></span>}
              <Stack gap={2} miw={0} style={{ flex: 1 }}>
                <Text fw={600} size="md" lh={1.35} className={s.itemTitle}>{it.title}</Text>
                {it.text && (it.markdown ? <Markdown text={it.text} /> : <Text size="sm" c="var(--deck-text-2)" lh={1.5}>{it.text}</Text>)}
                {it.meta && <Text size="xs" c="dimmed" className={s.meta}>{it.meta}</Text>}
              </Stack>
            </Group>
            {it.details && <Details text={it.details} />}
            {it.questions && <QuestionPicker questions={it.questions} act={answer} />}
            <Actions actions={rest} />
          </Stack>
        );
      })}
    </Stack>
  );
}
