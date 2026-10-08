import { useEffect, useRef } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Group, Stack, Text } from '@mantine/core';
import s from './list.module.css';
import { show } from '../lib/format';
import { Actions } from '../ui/Actions';
import { Deadline } from '../ui/Deadline';
import { Details } from '../ui/Details';
import { Markdown } from '../ui/Markdown';
import { NamedIcon } from '../ui/NamedIcon';
import { QuestionPicker } from '../ui/QuestionPicker';
import { rowOpen } from '../ui/rowOpen';
import type { Data } from '../lib/catalog.gen';
import { useScreenActions } from '../screen/ScreenActions';
import { dropList, keepList } from '../screen/decisions';
import { blockImpl } from './blockImpl';
import { useBlob } from './media/useBlob';

export type Item = NonNullable<Data['items']>[number];

// list draws items: a title with its icon in the color of its tone, text, meta with the time its buttons have left,
// details, the questions of an agent and buttons.
export const list = blockImpl('list', (_, d, context) => <Items items={d.items ?? []} context={context} />);

function Items({ items, context }: { items: Item[]; context: ComponentContext }) {
  const id = context.componentModel.id;
  const at = useRef<HTMLDivElement>(null);
  useEffect(() => keepList(id, items, at.current!), [id, items]);
  useEffect(() => () => dropList(id), [id]);
  return <div ref={at} className={s.items}>{items.map((it, i) => <Line key={show(it.id) || i} it={it} />)}</div>;
}

// Line is one item of a list.
export function Line({ it, big }: { it: Item; big?: boolean }) {
  const { run } = useScreenActions();
  const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
  const rest = (it.actions ?? []).filter((a) => a !== answer);
  const opens = rowOpen(it.actions, run);
  const until = (it.actions ?? []).map((a) => a.until).filter(Boolean).sort()[0];
  const mark = it.image ? <Thumb url={it.image} alt={it.title} />
    : it.icon ? <span className={s.itemIcon}><NamedIcon name={it.icon} fallback={it.title} size={17} /></span>
      : it.tone && it.tone !== 'muted' ? <span className={s.dot} /> : null;
  return (
    <div className={s.item} data-item data-tone={it.tone} data-big={big || undefined} data-marked={mark ? true : undefined} {...opens}>
      <Group gap={12} wrap="nowrap" align="flex-start">
        {mark}
        <Stack gap={2} miw={0} style={{ flex: 1 }}>
          <Text fw={600} lh={1.35} className={s.itemTitle}>{it.title}</Text>
          {it.text && (it.markdown ? <Markdown text={it.text} /> : <Text size="sm" c="var(--deck-text-2)" lh={1.45}>{it.text}</Text>)}
          {(it.meta || until) && (
            <Group gap={8} wrap="wrap" mt={2}>
              {until && <Deadline until={until} />}
              {it.meta && <Text size="xs" c="dimmed" className={s.meta}>{it.meta}</Text>}
            </Group>
          )}
        </Stack>
      </Group>
      <div className={s.under}>
        {it.details && <Details text={it.details} open={big} />}
        {it.questions && <QuestionPicker questions={it.questions} act={answer} />}
        <Actions actions={rest} max={big ? 4 : 3} />
      </div>
    </div>
  );
}

// Thumb is the picture of a line, read with the token like any file of deck.
function Thumb({ url, alt }: { url: string; alt: string }) {
  const { src } = useBlob(url);
  return src ? <img src={src} alt={alt} className={s.thumb} data-line-image /> : <span className={s.thumb} />;
}
