import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import GridLayout, { noCompactor, useContainerWidth, type Layout } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import { Button, Text, Title, useComputedColorScheme } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconCheck, IconSparkles, IconSun } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { NavStatus } from '../app/NavStatus';
import { dropParam, href, useRoute } from '../app/router';
import { openTopic } from '../chat/chatStore';
import { useLiveRead } from '../hooks/useLiveRead';
import { useMenuData, type MenuEntry } from '../hooks/useMenu';
import { call } from '../lib/api';
import { useCover } from '../lib/covers';
import { device } from '../lib/device';
import { notifyError } from '../lib/notify';
import { outcome } from '../screen/act';
import { NamedIcon } from '../ui/NamedIcon';
import s from './apps.module.css';
import { CardDeki } from './CardDeki';
import { derive, least, TODAY, type Cell, type Saved } from './layout';
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
  const style = { '--hue': hue(seed), ...(cover && color ? { '--cover-color': color } : {}) } as CSSProperties;
  return (
    <div className={s.card} data-card data-app-tile data-big={big || undefined} data-plain={plain || undefined} data-cover={cover && !plain ? true : undefined}
      style={style} {...data}>
      {src && !plain && <img src={src} alt="" className={s.cover} data-shown={shown || undefined} onLoad={() => setShown(true)} data-cover-img />}
      <a href={to} className={s.open} aria-label={title} data-app={seed} />
      {!plain && <span className={s.shade} />}
      <span className={s.body}>{children}</span>
      {art}
      {extra}
    </div>
  );
}

const open = (e: MenuEntry) => href({ project: e.project, screen: (e.open ?? e.screens[0] ?? e.extra?.[0])?.name ?? 'settings' });

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

// useCell is the size of a cell of the tiles in pixels, from the tokens of the theme: least width, height and gap.
function useCell(ref: React.RefObject<HTMLDivElement | null>) {
  const [cell, setCell] = useState({ min: 200, height: 180, gap: 24 });
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

// Apps is the home of deck: cards in cells where the owner put them, Today with Deki first by default; a long press
// opens the placing, where a card moves to any cell and grows or shrinks by its corner.
export function Apps() {
  const { t } = useTranslation();
  const menu = useMenuData();
  const route = useRoute();
  const phone = useMediaQuery('(max-width: 36em)') ?? false;
  const dark = useComputedColorScheme('light') === 'dark';
  const { width, containerRef, mounted } = useContainerWidth();
  const cell = useCell(containerRef);
  const cols = Math.max(1, Math.floor((width + cell.gap) / (cell.min + cell.gap)));
  const me = device();
  const saved = useLiveRead<Saved>('deck.layout', { device: me, columns: cols }, ['settings', 'projects'], EMPTY, !mounted);
  const ids = [TODAY, ...menu.projects.map((p) => p.project ?? '')];
  const [placing, setPlacing] = useState(route.params?.edit === '1');
  const [moved, setMoved] = useState<Cell[]>();
  useEffect(() => { if (route.params?.edit) dropParam('edit'); }, [route.params?.edit]);
  // Saves go one after another; what was moved stays drawn until deck gives the layout of the last of them.
  const saving = useRef<{ queue: Promise<unknown>; left: number }>({ queue: Promise.resolve(), left: 0 });
  useEffect(() => { if (!saving.current.left) setMoved(undefined); }, [saved, cols]);
  const cells = moved ?? derive(saved, ids, cols);
  const keep = (layout: Layout) => {
    const next = layout.map(({ i, x, y, w, h }) => ({ i, x, y, w, h }));
    setMoved(next);
    const q = saving.current;
    q.left++;
    q.queue = q.queue.then(() => call('deck.layout_set', { device: me, columns: cols, cards: JSON.stringify(next.map(({ i, ...c }) => ({ id: i, ...c }))) }))
      .catch((e) => notifyError(e, t('apps.place')))
      .finally(() => { q.left--; });
  };
  // A press held still opens the placing; a press beside the cards or Escape closes it.
  const hold = useRef<number>(undefined);
  const start = useRef<{ x: number; y: number }>(undefined);
  const down = (e: React.PointerEvent) => {
    const onCard = (e.target as HTMLElement).closest('[data-cell]');
    if (placing) {
      if (!onCard && !(e.target as HTMLElement).closest('[data-placing-done]')) setPlacing(false);
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
    const ask = placing && roomy(p.widgets, w, h) && (
      <Button size="compact-xs" variant="default" className={s.draw} leftSection={<IconSparkles size={13} />} data-widget-draw
        onClick={() => void call('deck.widget_draw', { project: id, size: `${w}x${h}` })
          .then((r) => { const o = outcome(r); if (o.topic) openTopic(o.topic); }, (e) => notifyError(e, t('apps.draw')))}>
        {t('apps.draw')}
      </Button>
    );
    if (view) {
      return (
        <Tile to={open(p)} title={p.title} seed={id} plain data={{ 'data-module': id }} extra={ask}>
          <span className={s.widgetHead}><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={16} /><Text size="sm" fw={700} truncate>{p.title}</Text></span>
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
      {placing && (
        <div className={s.placingBar}>
          <Text size="sm" c="dimmed">{t('apps.placing')}</Text>
          <Button size="xs" leftSection={<IconCheck size={14} />} onClick={() => setPlacing(false)} data-placing-done>{t('apps.done')}</Button>
        </div>
      )}
      <div ref={containerRef} className={s.room} data-cols={cols}>
        {mounted && (
          <GridLayout width={width} layout={cells.map((c) => ({ ...c, minW: least(c.i, cols).w, minH: 1, maxW: cols }))} compactor={noCompactor}
            gridConfig={{ cols, rowHeight: phone ? cell.height * 0.8 : cell.height, margin: [cell.gap, cell.gap], containerPadding: [0, 0] }}
            dragConfig={{ enabled: placing, threshold: 4 }} resizeConfig={{ enabled: placing, handles: ['se'] }}
            onDragStop={(layout) => keep(layout)} onResizeStop={(layout) => keep(layout)}>
            {cells.map((c) => {
              const p = menu.projects.find((x) => x.project === c.i);
              if (c.i === TODAY) {
                return (
                  <div key={c.i} data-cell={c.i}>
                    <Tile to={href({ screen: 'cockpit' })} title={t('nav.cockpit')} seed="cockpit" big art={<CardDeki size={phone ? 96 : Math.min(150, cell.height * 0.8)} />}>
                      <span className={s.today}><IconSun size={15} aria-hidden />{t('nav.cockpit')}</span>
                      <Widget screen="today" size={`${c.w}x${c.h}`} />
                    </Tile>
                  </div>
                );
              }
              return <div key={c.i} data-cell={c.i}>{p && draw(c.i, p, c.w, c.h)}</div>;
            })}
          </GridLayout>
        )}
      </div>
    </div>
  );
}
