import { useState, type CSSProperties, type ReactNode } from 'react';
import { ActionIcon, Button, Group, Text, Title, useComputedColorScheme } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconArrowLeft, IconArrowRight, IconArrowsSort, IconCheck, IconPlus, IconSun } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { NavStatus } from '../app/NavStatus';
import { href } from '../app/router';
import { useMenuData, type MenuEntry } from '../hooks/useMenu';
import { call } from '../lib/api';
import { notifyError } from '../lib/notify';
import { NamedIcon } from '../ui/NamedIcon';
import s from './apps.module.css';
import { CardDeki } from './CardDeki';
import { useCover } from './covers';

// hue is a color of a tile without a picture, the same for a name on every device.
function hue(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

// Tile is one tile of the grid: a link over it all, its picture fading in over its main color, and its words.
function Tile({ to, title, cover, color, seed, big, art, extra, children, data }: {
  to: string; title: string; cover?: string; color?: string; seed: string; big?: boolean; art?: ReactNode; extra?: ReactNode;
  children: ReactNode; data?: Record<string, string>;
}) {
  const src = useCover(cover);
  const [shown, setShown] = useState(false);
  const style = { '--hue': hue(seed), ...(cover && color ? { '--cover-color': color } : {}) } as CSSProperties;
  return (
    <div className={s.card} data-card data-app-tile data-big={big || undefined} data-cover={cover ? true : undefined} style={style} {...data}>
      {src && <img src={src} alt="" className={s.cover} data-shown={shown || undefined} onLoad={() => setShown(true)} data-cover-img />}
      <a href={to} className={s.open} aria-label={title} data-app={seed} />
      <span className={s.shade} />
      <span className={s.body}>{children}</span>
      {art}
      {extra}
    </div>
  );
}

const open = (e: MenuEntry) => href({ project: e.project, screen: (e.open ?? e.screens[0] ?? e.extra?.[0])?.name ?? 'settings' });

// Order are the arrows of a tile while the owner orders the modules: one place earlier or later.
function Order({ i, n, waiting, move }: { i: number; n: number; waiting: boolean; move: (i: number, by: number) => void }) {
  const { t } = useTranslation();
  return (
    <span className={s.order} data-order>
      <ActionIcon variant="default" size={30} disabled={waiting || i === 0} onClick={() => move(i, -1)} aria-label={t('nav.earlier')} data-move="up">
        <IconArrowLeft size={16} />
      </ActionIcon>
      <ActionIcon variant="default" size={30} disabled={waiting || i === n - 1} onClick={() => move(i, 1)} aria-label={t('nav.later')} data-move="down">
        <IconArrowRight size={16} />
      </ActionIcon>
    </span>
  );
}

// Apps is the home of deck: Today with Deki first, the modules in the owner's order with their pictures or icons, a
// new module, and the screens of deck and the master below, the master's settings opening from Settings.
export function Apps() {
  const { t } = useTranslation();
  const menu = useMenuData();
  const phone = useMediaQuery('(max-width: 36em)') ?? false;
  const dark = useComputedColorScheme('light') === 'dark';
  const [ordering, setOrdering] = useState(false);
  const shown = menu.projects.map((m) => m.project ?? '');
  // sent is the order a move was pressed on; the arrows wait until deck gives the next one.
  const [sent, setSent] = useState<string>();
  const waiting = sent === shown.join(' ');
  const move = (i: number, by: number) => {
    const names = [...shown];
    [names[i], names[i + by]] = [names[i + by], names[i]];
    setSent(shown.join(' '));
    call('deck.menu_order', { projects: names }).catch((e) => { setSent(undefined); notifyError(e, t('nav.order')); });
  };
  const links = menu.system.flatMap((g) => g.links).filter((l) => !(l.project && l.project === menu.master?.project && l.name === 'settings'));
  const { asks } = menu.pulse;
  const head = asks === 1 ? t('today.one') : asks > 1 ? t('today.many', { n: asks }) : t('today.none');
  return (
    <div className={s.apps} data-apps data-ordering={ordering || undefined}>
      <div className={s.grid}>
        <Tile to={href({ screen: 'cockpit' })} title={t('nav.cockpit')} seed="cockpit" big art={<CardDeki size={phone ? 104 : 150} />}>
          <span className={s.today}><IconSun size={15} aria-hidden />{t('nav.cockpit')}</span>
          <Title order={2} className={s.title} data-today-title>{head}</Title>
          <Text size="sm" className={s.text} lineClamp={2} data-today-line>{menu.pulse.line}</Text>
        </Tile>
        {menu.projects.map((p, i) => {
          const cover = dark ? p.cover_dark || p.cover : p.cover || p.cover_dark;
          return (
            <Tile key={p.project} to={open(p)} title={p.title} cover={cover} color={dark ? p.cover_dark_color || p.cover_color : p.cover_color || p.cover_dark_color}
              seed={p.project ?? p.title} extra={ordering && <Order i={i} n={menu.projects.length} waiting={waiting} move={move} />}
              data={{ 'data-module': p.project ?? '' }}>
              {!cover && <span className={s.icon} data-app-tile-icon><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={24} /></span>}
              <Title order={3} className={s.title}>{p.title}</Title>
              <NavStatus m={p} words />
            </Tile>
          );
        })}
        <a href={href({ screen: 'apps', params: { chat: 'new' } })} className={s.add} data-new-module>
          <span className={s.addIcon}><IconPlus size={20} /></span>
          <span className={s.addWords}>
            <Text fw={700} size="lg">{t('apps.new')}</Text>
            <Text size="sm" c="dimmed">{t('apps.new_text')}</Text>
          </span>
        </a>
      </div>
      {menu.projects.length > 1 && (
        <Group justify="center">
          <Button variant="subtle" color="gray" size="compact-sm" leftSection={ordering ? <IconCheck size={14} /> : <IconArrowsSort size={14} />}
            onClick={() => setOrdering(!ordering)} aria-pressed={ordering} data-apps-order>
            {t(ordering ? 'nav.order_done' : 'nav.order')}
          </Button>
        </Group>
      )}
      {links.length > 0 && (
        <nav className={s.links} aria-label={t('nav.control')} data-apps-links>
          {links.map((l) => (
            <a key={(l.project ?? '') + l.name} href={href({ project: l.project, screen: l.name })} className={s.link}>
              <NamedIcon name={l.icon ?? 'point'} fallback={l.title} size={16} />
              <span>{l.title}</span>
            </a>
          ))}
        </nav>
      )}
    </div>
  );
}
