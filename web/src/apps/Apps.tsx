import type { CSSProperties, PointerEvent, ReactNode } from 'react';
import { Text, Title } from '@mantine/core';
import { IconLayoutDashboard } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { NavStatus } from '../app/NavStatus';
import { href } from '../app/router';
import { useBlob } from '../blocks/media/useBlob';
import { useMenuData, type MenuEntry } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import s from './apps.module.css';

// TILT is how many degrees a card leans away from the pointer at its edge.
const TILT = 7;

// lean tilts a card away from the pointer over it; the styles keep it still on touch and without motion.
function lean(e: PointerEvent<HTMLAnchorElement>) {
  if (e.pointerType !== 'mouse') return;
  const r = e.currentTarget.getBoundingClientRect();
  const x = (e.clientX - r.left) / r.width - 0.5;
  const y = (e.clientY - r.top) / r.height - 0.5;
  e.currentTarget.style.setProperty('--ry', `${(x * TILT * 2).toFixed(2)}deg`);
  e.currentTarget.style.setProperty('--rx', `${(-y * TILT * 2).toFixed(2)}deg`);
}

function still(e: PointerEvent<HTMLAnchorElement>) {
  e.currentTarget.style.removeProperty('--ry');
  e.currentTarget.style.removeProperty('--rx');
}

// hue is a color of a card without a picture, the same for a name on every device.
function hue(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

function Card({ to, title, cover, seed, big, children }: { to: string; title: string; cover?: string; seed: string; big?: boolean; children: ReactNode }) {
  const { src } = useBlob(cover);
  const style = { '--hue': hue(seed), ...(src ? { backgroundImage: `url(${src})` } : {}) } as CSSProperties;
  return (
    <a href={to} className={s.card} data-big={big || undefined} data-cover={src ? true : undefined} style={style}
      onPointerMove={lean} onPointerLeave={still} aria-label={title} data-app={seed}>
      <span className={s.shade} />
      <span className={s.body}>{children}</span>
    </a>
  );
}

const open = (e: MenuEntry) => href({ project: e.project, screen: (e.open ?? e.screens[0] ?? e.extra?.[0])?.name ?? 'settings' });

// Apps is deck as cards instead of the side menu: the cockpit first, then the modules in the menu's order with their
// pictures, then the screens of deck and the master, the master's settings opening from Settings as in the menu.
export function Apps() {
  const { t } = useTranslation();
  const menu = useMenuData();
  const links = menu.system.flatMap((g) => g.links).filter((l) => !(l.project && l.project === menu.master?.project && l.name === 'settings'));
  return (
    <div className={s.apps} data-apps>
      <div className={s.grid}>
        <Card to={href({ screen: 'cockpit' })} title={t('nav.cockpit')} seed="cockpit" big>
          <span className={s.icon}><IconLayoutDashboard size={26} /></span>
          <Title order={2} className={s.title}>{t('nav.cockpit')}</Title>
          {menu.pulse.title && <Text size="sm" className={s.text} lineClamp={2}>{menu.pulse.title}</Text>}
          <NavStatus m={{ title: '', screens: [], asks: menu.pulse.asks, running: menu.pulse.running, errors: menu.pulse.errors }} />
        </Card>
        {menu.projects.map((p) => (
          <Card key={p.project} to={open(p)} title={p.title} cover={p.cover} seed={p.project ?? p.title}>
            <span className={s.icon}><NamedIcon name={p.icon ?? 'box'} fallback={p.title} size={24} /></span>
            <Title order={3} className={s.title}>{p.title}</Title>
            <NavStatus m={p} />
          </Card>
        ))}
      </div>
      {links.length > 0 && (
        <nav className={s.links} aria-label={t('nav.control')} data-apps-links>
          {links.map((l) => (
            <a key={(l.project ?? '') + l.name} href={href({ project: l.project, screen: l.name })} className={s.link}>
              <NamedIcon name={l.icon ?? 'point'} fallback={l.title} size={18} />
              <span>{l.title}</span>
            </a>
          ))}
        </nav>
      )}
    </div>
  );
}
