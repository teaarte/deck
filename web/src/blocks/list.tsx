import { useContext, useEffect, useState, type DragEvent } from 'react';
import { Group, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconAlertCircle, IconAlertTriangle, IconArrowRight, IconCheck } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './list.module.css';
import { relative, show, when } from '../lib/format';
import { useClock } from '../hooks/useClock';
import { ActionButton } from '../ui/actions/ActionButton';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { Actions } from '../ui/actions/Actions';
import { Holding } from '../ui/actions/undo';
import { verbIcon } from '../ui/actions/verbIcons';
import { useVisibleActions } from '../ui/actions/useVisibleActions';
import { Deki, useMascot } from '../ui/deki/Deki';
import { Cover } from '../ui/Cover';
import { Deadline } from '../ui/Deadline';
import { Details } from '../ui/Details';
import { NamedIcon } from '../ui/NamedIcon';
import { QuestionPicker } from '../ui/QuestionPicker';
import { opener, rowOpen } from '../ui/rowOpen';
import type { Data } from '../lib/catalog.gen';
import { press, useBlockContext } from '../screen/press';
import { Fit } from './around';
import { blockImpl } from './blockImpl';
import { Virtual } from '../ui/Virtual';
import { locale } from '../lib/i18n';

export type Item = NonNullable<Data['items']>[number];

// list draws items: a title with its icon in the color of its tone, text, meta, time, details, questions and buttons;
// as cards, a board, one at a time with focus, or chips when no item has words under its title and each one button.
export const list = blockImpl<{ cards?: boolean; focus?: boolean }>('list', (p, d) => <Listed cards={p.cards} focus={p.focus} d={d} />, {
  empty: (p, d) => (p.focus ? <Done text={d.empty} /> : undefined),
  bare: (p, d) => (p.focus ? 'plain' : !p.cards && !d?.board && !!d?.items?.length && d.items.every(chip) ? 'row' : undefined),
});

function Listed({ cards, focus, d }: { cards?: boolean; focus?: boolean; d: Data }) {
  const fit = useContext(Fit);
  const items = d.items ?? [];
  if (fit) return <div data-fit-lines>{items.map((it, i) => <FitLine key={show(it.id) || i} it={it} />)}</div>;
  if (d.board) return <Board board={d.board} items={items} />;
  if (focus) return <Focus items={items} empty={d.empty} />;
  if (!cards && items.every(chip)) return <Chips items={items} />;
  return <Items items={items} cards={cards} />;
}

type BoardData = NonNullable<Data['board']>;

// Board lays the items in lanes of their field, each no higher than the screen; dropping one in a lane presses its
// button whose input is that field with the value of the lane, which on a phone picks the lane itself.
function Board({ board, items }: { board: BoardData; items: Item[] }) {
  const ctx = useBlockContext();
  const [moved, setMoved] = useState<Record<string, string>>({});
  useEffect(() => setMoved({}), [items]);
  const shown = items;
  const mover = (it: Item) => it.actions?.find((a) => a.input === board.field && a.command);
  const laneOf = (it: Item) => moved[show(it.id)] ?? it.lane ?? '';
  const movable = shown.some((it) => mover(it));
  const drop = (lane: string) => (e: DragEvent) => {
    e.preventDefault();
    const id = e.dataTransfer.getData('text/deck-item');
    const it = shown.find((x) => show(x.id) === id);
    const act = it && mover(it);
    if (!it || !act || laneOf(it) === lane) return;
    setMoved((m) => ({ ...m, [id]: lane }));
    void press(ctx, act, { [board.field]: lane }).then((r) => {
      if (r.error) setMoved((m) => {
        const { [id]: _, ...rest } = m;
        return rest;
      });
    });
  };
  // The one accent of the board is on the first card of the first lane that has one, never on a muted card.
  const lead = board.lanes.flatMap((l) => shown.filter((it) => laneOf(it) === l.value)).find((it) => it.tone !== 'muted');
  return (
    <div className={s.board} data-board={board.field}>
      {board.lanes.filter((l) => movable || shown.some((it) => laneOf(it) === l.value)).map((l) => {
        const here = shown.filter((it) => laneOf(it) === l.value);
        return (
          <div key={l.value} className={s.lane} data-lane={l.value} onDragOver={(e) => e.preventDefault()} onDrop={drop(l.value)}>
            <Group justify="space-between" className={s.laneHead}><Text fw={700} size="sm">{l.label}</Text><Text size="xs" c="dimmed">{here.length}</Text></Group>
            {here.map((it, i) => (
              <div key={show(it.id) || i} className={s.card} draggable={!!mover(it)} data-card={show(it.id)}
                onDragStart={(e) => e.dataTransfer.setData('text/deck-item', show(it.id))}>
                <Line it={it} quiet={it !== lead} />
              </div>
            ))}
          </div>
        );
      })}
    </div>
  );
}

function Items({ items, cards }: { items: Item[]; cards?: boolean }) {
  const line = (it: Item, i: number) => <Line key={show(it.id) || i} it={it} quiet={i > 0} />;
  if (items.some((it) => it.group)) return <Groups items={items} cards={cards} line={line} />;
  if (cards) return <div className={s.cards} data-cards>{items.map(line)}</div>;
  return <Virtual items={items} row={56} gap={0} className={s.items} render={line} />;
}

// Groups are the items under the heading of their group, which the server put together.
function Groups({ items, cards, line }: { items: Item[]; cards?: boolean; line: (it: Item, i: number) => React.ReactNode }) {
  const groups: { name: string; items: Item[] }[] = [];
  items.forEach((it) => {
    const name = it.group ?? '';
    if (groups.at(-1)?.name !== name) groups.push({ name, items: [] });
    groups.at(-1)!.items.push(it);
  });
  return (
    <Stack gap="md" data-groups>
      {groups.map((g, gi) => (
        <section key={g.name || gi} data-group={g.name}>
          {g.name && <Text size="sm" fw={700} className={s.groupHead} data-group-head>{g.name}</Text>}
          <div className={cards ? s.cards : s.items} data-cards={cards || undefined}>{g.items.map(line)}</div>
        </section>
      ))}
    </Stack>
  );
}

const key = (it: Item) => show(it.id);

// Focus shows the items one at a time, the next two edges behind it: Later puts the one shown last on this device,
// and one whose press waits to go steps aside until it goes or is taken back.
function Focus({ items, empty }: { items: Item[]; empty?: string }) {
  const { t } = useTranslation();
  const [later, setLater] = useState<string[]>([]);
  const [held, setHeld] = useState<string[]>([]);
  useEffect(() => setHeld((h) => h.filter((k) => items.some((it) => key(it) === k))), [items]);
  const live = items.filter((it) => !held.includes(key(it)));
  const order = [...live.filter((it) => !later.includes(key(it))), ...later.flatMap((k) => live.filter((it) => key(it) === k))];
  const it = order[0];
  if (!it) return <Done text={empty} />;
  const hold = (k: string) => (on: boolean) => setHeld((h) => (on ? [...h, k] : h.filter((x) => x !== k)));
  const next = order.length > 1 ? () => setLater([...later.filter((k) => k !== key(it)), key(it)]) : undefined;
  return (
    <div className={s.stack} data-focus data-left={order.length}>
      <div className={s.focusCard} key={key(it)} data-focus-card={key(it)}>
        <Group justify="space-between" gap="xs" className={s.focusHead}>
          <Text size="xs" fw={700} className={s.kind} data-kind>{it.kind}</Text>
          <Text size="xs" fw={600} c="dimmed" data-of>{t('list.one_of', { n: 1, of: order.length })}</Text>
        </Group>
        <Holding.Provider value={hold(key(it))}>
          <Line it={it} big max={3} later={next} />
        </Holding.Provider>
      </div>
      {order.length > 1 && <div className={s.edge} data-edge="1" />}
      {order.length > 2 && <div className={s.edge} data-edge="2" />}
    </div>
  );
}

// Done is a focus list with nothing left: Deki at rest and its empty text.
function Done({ text }: { text?: string }) {
  const { t } = useTranslation();
  const mascot = useMascot();
  return (
    <Stack align="center" gap="xs" className={s.done} data-focus-done>
      {mascot && <Deki pose="done" size={150} />}
      <Text fz={20} fw={700} ta="center">{text || t('state.empty')}</Text>
    </Stack>
  );
}

// chip is an item drawn as a chip: no words under its title and one button beside its way to open.
const chip = (it: Item) => !it.text && !it.meta && !it.questions && (it.actions ?? []).filter((a) => !(a.verb === 'open' && a.screen)).length === 1;

// Chips are items in a row: what runs, each with its icon, how long and its one button, such as Stop.
function Chips({ items }: { items: Item[] }) {
  return <div className={s.chips} data-chips>{items.map((it, i) => <Chip key={key(it) || i} it={it} />)}</div>;
}

function Chip({ it }: { it: Item }) {
  const ctx = useBlockContext();
  const [act] = useVisibleActions((it.actions ?? []).filter((a) => !(a.verb === 'open' && a.screen)));
  return (
    <div className={s.chip} data-chip data-tone={it.tone} title={it.details} {...rowOpen(it.actions, ctx)}>
      {it.icon && <span className={s.chipIcon}><NamedIcon name={it.icon} fallback={it.title} size={15} /></span>}
      <Text size="sm" fw={600} truncate>{it.title}</Text>
      {it.since && <Since at={it.since} />}
      {act && <ActionIconButton act={act} icon={verbIcon(act)} variant="default" />}
    </div>
  );
}

// TONES are the icons of a tone for a line without its own icon, so a tone is not color alone.
const TONES: Record<string, typeof IconCheck> = { ok: IconCheck, warn: IconAlertTriangle, error: IconAlertCircle };

// mark is the icon of a line: its own, else that of its tone, so a tone is not color alone.
function mark(it: Item) {
  const Tone = it.tone ? TONES[it.tone] : undefined;
  return it.icon ? <span className={s.itemIcon}><NamedIcon name={it.icon} fallback={it.title} size={17} /></span>
    : Tone ? <span className={s.itemIcon} data-tone-icon><Tone size={17} /></span> : null;
}

// FitLine is an item in a widget: one line of its title and one of its time, text and meta, its first button beside
// them, words cut at the end rather than wrapped; a narrow line shows the button as its icon.
function FitLine({ it }: { it: Item }) {
  const ctx = useBlockContext();
  const open = opener(it.actions);
  // A plain open button goes, as on a screen: a click on the line opens it.
  const [act] = useVisibleActions((it.actions ?? []).filter((a) => (!a.input || a.primary) && !(a === open && a.verb === 'open' && !a.primary)));
  const under = [it.text, it.meta].filter(Boolean).join(' · ');
  return (
    <div className={s.fitLine} data-item data-tone={it.tone} {...rowOpen(it.actions, ctx)}>
      {it.cover ? <Cover cover={it.cover} className={s.fitCover} /> : mark(it)}
      <div className={s.fitWords}>
        <Text fw={600} size="sm" truncate className={s.fitTitle}>{it.title}</Text>
        {(it.since || under) && (
          <Text size="xs" truncate className={`${s.meta} ${s.fitUnder}`}>
            {it.since && <Since at={it.since} inline />}{it.since && under ? ' · ' : ''}{under}
          </Text>
        )}
      </div>
      {act && (
        <span className={s.fitAct}>
          <span className={s.fitWide}><ActionButton act={act} quiet /></span>
          <span className={s.fitNarrow}><ActionIconButton act={act} icon={verbIcon(act)} variant="default" /></span>
        </span>
      )}
    </div>
  );
}

// Line is one item of a list; later is the way to put it last, in a focus list.
export function Line({ it, big, quiet, max, later }: { it: Item; big?: boolean; quiet?: boolean; max?: number; later?: () => void }) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
  const open = opener(it.actions);
  // A plain open button goes: a click on the line opens it.
  const rest = (it.actions ?? []).filter((a) => a !== answer && !(a === open && a.verb === 'open' && !a.primary));
  const opens = rowOpen(it.actions, ctx);
  const until = (it.actions ?? []).map((a) => a.until).filter(Boolean).sort()[0];
  const marked = mark(it);
  return (
    <div className={s.item} data-item data-tone={it.tone} data-big={big || undefined} data-marked={marked ? true : undefined}
      title={it.since ? when(it.since)?.toLocaleString(locale()) : undefined} {...opens}>
      {it.cover && <Cover cover={it.cover} className={s.cover} />}
      <Group gap={12} wrap="nowrap" align="flex-start">
        {marked}
        <Stack gap={2} miw={0} style={{ flex: 1 }}>
          <Text fw={600} lh={1.35} className={s.itemTitle}>{it.title}</Text>
          {it.text && <Text size="sm" c="var(--deck-text-2)" lh={1.45}>{it.text}</Text>}
          {(it.meta || it.since || until) && (
            <Group gap={8} wrap="wrap" mt={2}>
              {until && <Deadline until={until} />}
              {it.since && <Since at={it.since} />}
              {it.meta && <Text size="xs" c="dimmed" className={s.meta}>{it.meta}</Text>}
            </Group>
          )}
        </Stack>
      </Group>
      <div className={s.under}>
        {it.details && <Details text={it.details} />}
        {it.questions && <QuestionPicker questions={it.questions} act={answer} quiet={quiet} />}
        {later ? (
          <Group gap={6} wrap="wrap">
            <Actions actions={rest} max={max ?? 4} quiet={quiet} />
            <UnstyledButton className={s.later} onClick={later} data-later>{t('list.later')}<IconArrowRight size={14} /></UnstyledButton>
          </Group>
        ) : <Actions actions={rest} max={max ?? (big ? 4 : 3)} quiet={quiet} />}
      </div>
    </div>
  );
}

// Since is how long ago the time a line counts from was, kept fresh.
function Since({ at, inline }: { at: string; inline?: boolean }) {
  const now = useClock(1000);
  const d = when(at);
  if (!d) return null;
  if (inline) return <span data-since>{relative(d, now)}</span>;
  return <Text size="xs" c="dimmed" className={s.meta} data-since>{relative(d, now)}</Text>;
}
