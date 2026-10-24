import { useState, type CSSProperties, type ReactNode } from 'react';
import { Text, Title } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconChevronDown, IconLayoutDashboard } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { NavStatus } from '../app/NavStatus';
import { href } from '../app/router';
import { useBlob } from '../blocks/media/useBlob';
import { placesOf, useMenuData, type MenuEntry, type Place } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import s from './apps.module.css';
import { CardDeki } from './CardDeki';

// hue is a color of a card without a picture, the same for a name on every device.
function hue(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

function Card({ to, title, cover, seed, big, places, art, children }: { to: string; title: string; cover?: string; seed: string; big?: boolean; places: Place[]; art?: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  const { src } = useBlob(cover);
  const [open, setOpen] = useState(false);
  const style = { '--hue': hue(seed), ...(src ? { backgroundImage: `url(${src})` } : {}) } as CSSProperties;
  return (
    <div className={s.card} data-big={big || undefined} data-open={open || undefined} style={style} onMouseLeave={() => setOpen(false)}>
      <a href={to} className={s.open} aria-label={title} data-app={seed} data-cover={src ? true : undefined} />
      <span className={s.shade} />
      <span className={s.body}>{children}</span>
      {art}
      {places.length > 1 && (
        <>
          <button type="button" className={s.more} onClick={() => setOpen(!open)} aria-label={t('nav.screens')} aria-expanded={open} data-card-more>
            <IconChevronDown size={18} />
          </button>
          <nav className={s.places} aria-label={t('nav.screens')} data-card-places>
            {places.map((p) => (
              <a key={p.name} href={href({ project: p.project, screen: p.name })} className={s.place}>
                <NamedIcon name={p.icon} fallback={p.title} size={14} />
                <span>{p.title}</span>
              </a>
            ))}
          </nav>
        </>
      )}
    </div>
  );
}

const open = (e: MenuEntry) => href({ project: e.project, screen: (e.open ?? e.screens[0] ?? e.extra?.[0])?.name ?? 'settings' });

// Apps is deck as cards instead of the side menu: the cockpit first, then the modules in the menu's order with their
// pictures, then the screens of deck and the master, the master's settings opening from Settings as in the menu.
export function Apps() {
  const { t } = useTranslation();
  const menu = useMenuData();
  const phone = useMediaQuery('(max-width: 36em)') ?? false;
  const links = menu.system.flatMap((g) => g.links).filter((l) => !(l.project && l.project === menu.master?.project && l.name === 'settings'));
  return (
    <div className={s.apps} data-apps>
      <div className={s.grid}>
        <Card to={href({ screen: 'cockpit' })} title={t('nav.cockpit')} seed="cockpit" big places={[]} art={<CardDeki size={phone ? 104 : 150} />}>
          <span className={s.icon}><IconLayoutDashboard size={26} /></span>
          <Title order={2} className={s.title}>{t('nav.cockpit')}</Title>
          {menu.pulse.title && <Text size="sm" className={s.text} lineClamp={2}>{menu.pulse.title}</Text>}
          <NavStatus m={{ title: '', screens: [], asks: menu.pulse.asks, running: menu.pulse.running, errors: menu.pulse.errors }} />
        </Card>
        {menu.projects.map((p) => (
          <Card key={p.project} to={open(p)} title={p.title} cover={p.cover} seed={p.project ?? p.title} places={placesOf(menu, p.project)}>
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
              <NamedIcon name={l.icon ?? 'point'} fallback={l.title} size={16} />
              <span>{l.title}</span>
            </a>
          ))}
        </nav>
      )}
    </div>
  );
}
