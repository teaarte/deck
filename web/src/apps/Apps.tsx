import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import GridLayout, { noCompactor, useContainerWidth, type Layout } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import { ActionIcon, Button, Menu, Text, TextInput, Title, useComputedColorScheme } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconCheck, IconDots, IconEyeOff, IconFolder, IconFolderPlus, IconFolderUp, IconPencil, IconSettings, IconSparkles, IconSun } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { of } from '../app/Backdrop';
import { NavStatus } from '../app/NavStatus';
import { dropParam, href, useMoving, useRoute } from '../app/router';
import { useLiveRead } from '../hooks/useLiveRead';
import { useMenuData, type MenuEntry } from '../hooks/useMenu';
import { useCover } from '../lib/covers';
import { notifyError } from '../lib/notify';
import { press } from '../screen/press';
import { useWorn } from '../theme/worn';
import { NamedIcon } from '../ui/NamedIcon';
import s from './apps.module.css';
import { CardDeki } from './CardDeki';
import { derive, FOLDER, foldersOf, least, TODAY, type Cell, type Saved } from './layout';
import { Widget } from './Widget';

// hue is a color of a tile without a picture, the same for a name on every device.
function hue(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

// Tile is one card of the tiles: a link over it all, its picture fading in over its main color, and its words.
function Tile({ to, title, cover, color, seed, big, plain, art, extra, children, data }: {
  to: string; title: string; cover?: string; color?: string; seed: string; big?: boolean; plain?: boolean; art?: ReactNode;
  extra?: ReactNode; children: ReactNode; data?: Record<string, string>;
}) {
  const src = useCover(cover);
  const [shown, setShown] = useState(false);
  const grows = useMoving() === seed;
  const style = { '--hue': hue(seed), ...(cover && color ? { '--cover-color': color } : {}), ...(grows ? { viewTransitionName: 'module' } : {}) } as CSSProperties;
  return (
    <div className={s.card} data-card data-app-tile data-big={big || undefined} data-plain={plain || undefined} data-cover={cover && !plain ? true : undefined}
      style={style} {...data}>
      {src && !plain && <img src={src} alt="" className={s.cover} data-shown={shown || undefined} onLoad={() => setShown(true)} data-cover-img />}
      {cover && !src && !plain && <span hidden data-awaits />}
      <a href={to} className={s.open} aria-label={title} data-app={seed} />
      {!plain && <span className={s.shade} />}
      <span className={s.body}>{children}</span>
      {art}
      {extra}
    </div>
  );
}

// CoverImg is the picture of a module's card for the room its empty view leaves.
function CoverImg({ at }: { at: string }) {
  const src = useCover(at);
  return src ? <img src={src} alt="" className={s.cover} data-shown /> : null;
}

// open is the page a card of a module opens: its first screen, or the card that enables a draft.
const open = (e: MenuEntry) => (e.draft ? href({ screen: 'draft', params: { project: e.project ?? '' } })
  : href({ project: e.project, screen: (e.open ?? e.screens[0] ?? e.extra?.[0])?.name ?? 'settings' }));

// fits is the largest view of the widgets of a module a card of a size fits, by its least size; none when none fits.
function fits(widgets: string[] | undefined, w: number, h: number): string | undefined {
  let best: string | undefined;
  let area = 0;
  for (const m of widgets ?? []) {
    const [mw, mh] = m.split('x').map(Number);
    if (mw <= w && mh <= h && mw * mh > area) [best, area] = [m, mw * mh];
  }
  return best;
}

// roomy says a card is much larger than the largest view of its module, or of two cells and more without one: Deki
// may draw a view for its size.
function roomy(widgets: string[] | undefined, w: number, h: number): boolean {
  const largest = Math.max(0, ...(widgets ?? []).map((m) => m.split('x').map(Number).reduce((a, b) => a * b, 1)));
  return largest === 0 ? w * h >= 2 : w * h >= 2 * largest;
}

// Room is the room the lines of Today leave under them, told by its height.
function Room({ onHeight }: { onHeight: (px: number) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const seen = new ResizeObserver(() => onHeight(el.clientHeight));
    seen.observe(el);
    return () => seen.disconnect();
  }, [onHeight]);
  return <div ref={ref} className={s.todayRoom} data-today-room />;
}

// seen are the cell and the width of the tiles last measured in this tab: the tiles drawn again stand at once.
const seen = { cell: { min: 200, height: 180, gap: 24 }, width: 0 };

// useCell is the size of a cell of the tiles in pixels, from the tokens of the theme: least width, height and gap.
function useCell(ref: React.RefObject<HTMLDivElement | null>) {
  const [cell, setCell] = useState(seen.cell);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const probe = document.createElement('div');
    probe.className = s.probe;
    el.appendChild(probe);
    const read = () => {
      const px = (name: string) => {
        probe.style.width = `var(${name})`;
        return probe.getBoundingClientRect().width;
      };
      const next = { min: px('--deck-tile-min') || 200, height: px('--deck-tile-height') || 180, gap: px('--deck-tile-gap') };
      seen.cell = next;
      setCell((c) => (c.min === next.min && c.height === next.height && c.gap === next.gap ? c : next));
    };
    read();
    const watch = new MutationObserver(read);
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ['style', 'data-deck-theme', 'data-deck-density'] });
    return () => { watch.disconnect(); probe.remove(); };
  }, [ref]);
  return cell;
}

const EMPTY: Saved = { columns: 0, own: false, cards: [] };
const NARROW = '(max-width: 36em)';

// Apps is the home of deck: cards in cells where the owner put them, Today with Deki first by default; a long press
// opens the placing, where a card moves to any cell and grows or shrinks by its corner.
export function Apps() {
  const { t } = useTranslation();
  const menu = useMenuData();
  const route = useRoute();
  // Known from the first draw, so the cells do not shift once the query answers.
  const phone = useMediaQuery(NARROW, matchMedia(NARROW).matches, { getInitialValueInEffect: false });
  const dark = useComputedColorScheme('light') === 'dark';
  const scene = useWorn().images?.cockpit;
  const { width, containerRef, mounted } = useContainerWidth(seen.width ? { initialWidth: seen.width } : undefined);
  if (mounted) seen.width = width;
  const measured = useCell(containerRef);
  // A phone keeps half the gap between its cards.
  const cell = phone ? { ...measured, gap: measured.gap / 2 } : measured;
  // The narrowest grid still has two columns, as a phone: a card across it is two cells wide, a half one.
  const cols = Math.max(2, Math.floor((width + cell.gap) / (cell.min + cell.gap)));
  const saved = useLiveRead<Saved>('deck.layout_get', { columns: cols }, EMPTY, !mounted);
  // held are the folders of the modules as the owner moved them, kept until deck gives the layout of the last save.
  const [held, setHeld] = useState<Record<string, string>>();
  const inFolder = held ?? foldersOf(saved);
  const named = (p: MenuEntry) => p.project ?? '';
  const folders = [...new Set(menu.projects.map(named).filter((id) => inFolder[id]).map((id) => inFolder[id]))];
  const ids = [TODAY, ...folders.map((f) => FOLDER + f), ...menu.projects.map(named).filter((id) => !inFolder[id])];
  const [placing, setPlacing] = useState(route.params?.edit === '1');
  const [room, setRoom] = useState(0);
  const [moved, setMoved] = useState<Cell[]>();
  useEffect(() => { if (route.params?.edit) dropParam('edit'); }, [route.params?.edit]);
  // Saves go one after another; what was moved stays drawn until deck gives the layout of the last of them.
  const saving = useRef<{ queue: Promise<unknown>; left: number }>({ queue: Promise.resolve(), left: 0 });
  useEffect(() => { if (!saving.current.left) { setMoved(undefined); setHeld(undefined); } }, [saved, cols]);
  const cells = (moved ?? derive(saved, ids, cols)).filter((c) => ids.includes(c.i));
  const keep = (layout: Layout | Cell[], into: Record<string, string> = inFolder) => {
    const next = layout.map(({ i, x, y, w, h }) => ({ i, x, y, w, h }));
    setMoved(next);
    setHeld(into);
    const q = saving.current;
    q.left++;
    const save = saved.save;
    // The modules of folders the menu shows go with the save; deck keeps those of paused modules itself.
    const shown = new Set(menu.projects.map(named));
    const cards = [...next.map(({ i, ...c }) => ({ id: i, ...c })),
      ...Object.entries(into).filter(([id]) => shown.has(id)).map(([id, f]) => ({ id, in: f, x: 0, y: 0, w: 1, h: 1 }))];
    q.queue = q.queue.then(async () => {
      const r = save ? await press(undefined, save, { cards: JSON.stringify(cards) }) : {};
      if (r.error) notifyError(r.error, t('The tiles could not be placed'));
    }).finally(() => { q.left--; });
  };
  // resize gives a card a size; the cards it now covers step down to the first free cells under it.
  const resize = (id: string, w: number, h: number) => keep(settle(cells.map((c) => (c.i === id ? { ...c, w: Math.min(w, cols), h } : c)), id, cols));
  // into puts a module in a folder, a new one standing where the module stood.
  const into = (id: string, folder: string) => {
    const at = cells.find((c) => c.i === id);
    const rest = cells.filter((c) => c.i !== id);
    const grid = folders.includes(folder) || !at ? rest : settle([...rest, { ...at, i: FOLDER + folder }], FOLDER + folder, cols);
    keep(grid, { ...inFolder, [id]: folder });
  };
  // out takes a module out of its folder to the first free cells; a folder left empty goes.
  const out = (id: string) => {
    const { [id]: _, ...rest } = inFolder;
    keep(cells, rest);
  };
  // A press held still opens the placing; a press beside the cards or Escape closes it.
  const hold = useRef<number>(undefined);
  const start = useRef<{ x: number; y: number }>(undefined);
  const down = (e: React.PointerEvent) => {
    const onCard = (e.target as HTMLElement).closest('[data-cell]');
    if (placing) {
      // A press in the menu of a card, drawn apart from the cards, keeps the placing.
      if (!onCard && !(e.target as HTMLElement).closest('[data-placing-done], [data-card-menu-list]')) setPlacing(false);
      return;
    }
    if (!onCard) return;
    start.current = { x: e.clientX, y: e.clientY };
    hold.current = window.setTimeout(() => { setPlacing(true); navigator.vibrate?.(10); }, 550);
  };
  const stop = () => window.clearTimeout(hold.current);
  const drift = (e: React.PointerEvent) => {
    if (start.current && Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > 8) stop();
  };
  useEffect(() => {
    if (!placing) return;
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setPlacing(false);
    window.addEventListener('keydown', esc);
    return () => window.removeEventListener('keydown', esc);
  }, [placing]);
  const draw = (id: string, p: MenuEntry, w: number, h: number) => {
    const view = fits(p.widgets, w, h);
    const cover = dark ? p.cover_dark || p.cover : p.cover || p.cover_dark;
    const asks = p.draw;
    const ask = placing && asks && roomy(p.widgets, w, h) && (
      <Button size="compact-xs" variant="default" className={s.draw} leftSection={<IconSparkles size={13} />} data-widget-draw
        onClick={() => void press(undefined, asks, { size: `${w}x${h}` }).then((r) => { if (r.error) notifyError(r.error, asks.label); })}>
        {asks.label}
      </Button>
    );
    if (view) {
      // A view with nothing to show gives way to the cover of the module, as a card without a view.
      return (
        <Tile to={open(p)} title={p.title} seed={id} plain={!cover} cover={cover} color={dark ? p.cover_dark_color || p.cover_color : p.cover_color || p.cover_dark_color}
          data={{ 'data-module': id, ...(cover ? { 'data-veiled': '' } : {}) }} extra={ask}
          art={<span className={s.emptyCover} aria-hidden>{cover ? <CoverImg at={cover} /> : <span className={s.icon}><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={24} /></span>}
            <Title order={3} className={s.title}>{p.title}</Title></span>}>
          <span className={s.widgetHead}><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={16} /><Text size="sm" fw={700} lineClamp={2} lh={1.25}>{p.title}</Text></span>
          <Widget project={id} screen="widget" size={`${w}x${h}`} />
        </Tile>
      );
    }
    return (
      <Tile to={open(p)} title={p.title} cover={cover} color={dark ? p.cover_dark_color || p.cover_color : p.cover_color || p.cover_dark_color}
        seed={id} data={{ 'data-module': id }} extra={ask}>
        {!cover && <span className={s.icon} data-app-tile-icon><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={24} /></span>}
        <Title order={3} className={s.title}>{p.title}</Title>
        <NavStatus m={p} words />
      </Tile>
    );
  };
  return (
    <div className={s.apps} data-apps data-placing={placing || undefined} onPointerDown={down} onPointerUp={stop} onPointerCancel={stop} onPointerMove={drift}
      onContextMenu={(e) => {
        // A long touch is the menu of a link on a phone: here it opens the placing instead.
        if (!(e.target as HTMLElement).closest('[data-cell]')) return;
        e.preventDefault();
        stop();
        setPlacing(true);
      }}>
      {saved === EMPTY && <span hidden data-awaits />}
      {placing && (
        <div className={s.placingBar}>
          <Text size="sm" c="dimmed">{t('Drag a card to any cell or pull its corner; its menu sets its size or puts it in a folder')}</Text>
        </div>
      )}
      <ActionIcon className={s.arrange} size={48} variant={placing ? 'filled' : 'default'} onClick={() => setPlacing(!placing)}
        aria-label={t('Arrange the cards')} aria-pressed={placing} title={t(placing ? 'Done' : 'Arrange the cards')} data-placing-toggle data-placing-done={placing || undefined}>
        {placing ? <IconCheck size={22} /> : <IconPencil size={22} />}
      </ActionIcon>
      <div ref={containerRef} className={s.room} data-cols={cols}>
        {mounted && (
          <GridLayout width={width} layout={cells.map((c) => ({ ...c, minW: least(c.i, cols).w, minH: 1, maxW: cols }))} compactor={noCompactor}
            gridConfig={{ cols, rowHeight: cell.height, margin: [cell.gap, cell.gap], containerPadding: [0, 0] }}
            dragConfig={{ enabled: placing, threshold: 4, cancel: '[data-card-menu], [data-out]' }} resizeConfig={{ enabled: placing, handles: ['se'] }}
            onDragStop={(layout) => keep(layout)} onResizeStop={(layout) => keep(layout)}>
            {cells.map((c) => {
              const p = menu.projects.find((x) => x.project === c.i);
              if (c.i === TODAY) {
                return (
                  <div key={c.i} data-cell={c.i}>
                    <Tile to={href({ screen: 'cockpit' })} title={t('Today')} seed="cockpit" big cover={of(scene, dark)}
                      color={(dark ? scene?.dark_color : undefined) || scene?.color} art={<CardDeki size={Math.min(phone ? 96 : 150, cell.height * 0.8)} room={room} />}>
                      <span className={s.today}><IconSun size={15} aria-hidden />{t('Today')}
                        <a href={href({ screen: 'settings' })} className={s.todaySettings} aria-label={t('Settings')} title={t('Settings')} data-today-settings><IconSettings size={16} /></a></span>
                      <Widget screen="today" size={`${c.w}x${c.h}`} rest={<Room onHeight={setRoom} />} />
                    </Tile>
                  </div>
                );
              }
              if (c.i.startsWith(FOLDER)) {
                const name = c.i.slice(FOLDER.length);
                return (
                  <div key={c.i} data-cell={c.i}>
                    <FolderTile name={name} modules={menu.projects.filter((m) => inFolder[named(m)] === name)} placing={placing} onOut={out} />
                    {placing && <CardMenu id={c.i} w={c.w} h={c.h} cols={cols} least={least(c.i, cols)} onSize={resize} />}
                  </div>
                );
              }
              return (
                <div key={c.i} data-cell={c.i}>
                  {p && draw(c.i, p, c.w, c.h)}
                  {placing && p && <CardMenu id={c.i} w={c.w} h={c.h} cols={cols} least={least(c.i, cols)} onSize={resize}
                    folders={folders} onFolder={(f) => into(c.i, f)} onHide={() => into(c.i, OFF)} />}
                </div>
              );
            })}
          </GridLayout>
        )}
      </div>
    </div>
  );
}

// OFF is the folder of the modules taken off the home, named in the owner's words where it shows.
const OFF = 'More';

// SIZES are the sizes a card takes from its menu, in cells.
const SIZES: [number, number][] = [[1, 1], [2, 1], [1, 2], [2, 2], [3, 2], [4, 2]];

// CardMenu is the pencil of a card while the owner arranges them: its size, its folder, or off the home.
function CardMenu({ id, w, h, cols, least: min, onSize, folders, onFolder, onHide }: {
  id: string; w: number; h: number; cols: number; least: { w: number; h: number }; onSize: (id: string, w: number, h: number) => void;
  folders?: string[]; onFolder?: (folder: string) => void; onHide?: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState('');
  const sizes = SIZES.filter(([sw, sh]) => sw <= cols && sw >= min.w && sh >= min.h);
  return (
    <Menu position="bottom-end" withinPortal shadow="md" closeOnItemClick>
      <Menu.Target>
        <ActionIcon className={s.cardMenu} size={32} variant="default" aria-label={t('Card')} data-card-menu={id}
          onPointerDown={(e) => e.stopPropagation()}><IconDots size={16} /></ActionIcon>
      </Menu.Target>
      <Menu.Dropdown miw={200} data-card-menu-list={id}>
        <Menu.Label>{t('Size')}</Menu.Label>
        <div className={s.sizes}>
          {sizes.map(([sw, sh]) => (
            <Button key={`${sw}x${sh}`} size="compact-xs" variant={sw === w && sh === h ? 'filled' : 'default'} onClick={() => onSize(id, sw, sh)} data-size={`${sw}x${sh}`}
              aria-pressed={sw === w && sh === h} aria-label={t('{{w}} by {{h}} cells', { w: sw, h: sh })}>{sw}×{sh}</Button>
          ))}
        </div>
        {onFolder && (
          <>
            <Menu.Divider />
            <Menu.Label>{t('Into a folder')}</Menu.Label>
            {(folders ?? []).map((f) => <Menu.Item key={f} leftSection={<IconFolder size={15} />} onClick={() => onFolder(f)} data-into={f}>{f === OFF ? t(OFF) : f}</Menu.Item>)}
            <div className={s.newFolder}>
              <TextInput size="xs" placeholder={t('New folder')} aria-label={t('New folder')} value={name} onChange={(e) => setName(e.currentTarget.value)} maxLength={40}
                onKeyDown={(e) => { if (e.key === 'Enter' && name.trim()) onFolder(name.trim()); }} data-new-folder
                rightSection={<ActionIcon size="sm" variant="subtle" disabled={!name.trim()} onClick={() => onFolder(name.trim())} aria-label={t('New folder')}><IconFolderPlus size={14} /></ActionIcon>} />
            </div>
          </>
        )}
        {onHide && <><Menu.Divider /><Menu.Item leftSection={<IconEyeOff size={15} />} onClick={onHide} data-hide>{t('Off the home')}</Menu.Item></>}
      </Menu.Dropdown>
    </Menu>
  );
}

// FolderTile is a folder of modules on the home: its name over a list of them, each a link; while the owner arranges
// the cards each comes out of it by its button.
function FolderTile({ name, modules, placing, onOut }: { name: string; modules: MenuEntry[]; placing: boolean; onOut: (id: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className={s.card} data-card data-app-tile data-folder={name} style={{ '--hue': hue(FOLDER + name) } as CSSProperties}>
      <div className={s.folder}>
        <span className={s.folderHead}><IconFolder size={16} /><Text size="sm" fw={700} truncate>{name === OFF ? t(OFF) : name}</Text></span>
        <div className={s.folderList}>
          {modules.map((m) => (
            <div key={m.project} className={s.folderRow}>
              <a href={open(m)} className={s.folderLink} data-folder-module={m.project}>
                <NamedIcon name={m.icon ?? 'box'} fallback={m.title} size={16} /><Text size="sm" truncate>{m.title}</Text>
              </a>
              {placing && <ActionIcon size="sm" variant="subtle" onClick={() => onOut(m.project ?? '')} aria-label={t('Out of the folder')} title={t('Out of the folder')}
                onPointerDown={(e) => e.stopPropagation()} data-out={m.project}><IconFolderUp size={14} /></ActionIcon>}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// settle keeps the card of id where it is and moves each card it covers to the first free cells from where it stood.
function settle(cells: Cell[], id: string, cols: number): Cell[] {
  const fixed = cells.find((c) => c.i === id);
  if (!fixed) return cells;
  const placed: Cell[] = [fixed];
  const hits = (a: Cell, b: Cell) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
  for (const one of [...cells].filter((c) => c.i !== id).sort((a, b) => a.y - b.y || a.x - b.x)) {
    const c = { ...one, w: Math.min(one.w, cols) };
    let at = c;
    for (let y = c.y; placed.some((p) => hits(p, at)) && y < c.y + 1000; y++) {
      const x = Array.from({ length: cols - c.w + 1 }, (_, k) => k).find((k) => !placed.some((p) => hits(p, { ...c, x: k, y })));
      if (x !== undefined) at = { ...c, x, y };
    }
    placed.push(at);
  }
  return placed;
}
