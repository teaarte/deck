import { memo, useEffect } from 'react';
import { Collapse, ScrollArea, Tooltip, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconChevronRight } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Menu as MenuData, MenuGroup } from '../hooks/useMenu';
import { useMasterName } from '../ui/deki/Deki';
import { NamedIcon } from '../ui/NamedIcon';
import s from './nav.module.css';
import { NavStatus } from './NavStatus';
import { href } from './router';

type Link = MenuGroup['links'][number];

// System is the groups at the foot of the menu, as deck gives them, behind one button that unfolds them on a tap:
// the work of deck, the screens of the master and deck's settings. The master's settings open from Settings.
function System({ menu, current, onPick }: { menu: MenuData; current: string; onPick?: () => void }) {
  const { t } = useTranslation();
  const name = useMasterName();
  const [open, setOpen] = useLocalStorage<boolean>({ key: 'deck-nav-foot', defaultValue: false });
  const path = current.split('?')[0];
  const quick = (l: Link) => !(l.project && l.project === menu.master?.project && l.name === 'settings');
  const groups = menu.system.map((g) => ({ ...g, links: g.links.filter(quick) })).filter((g) => g.links.length > 0);
  const here = groups.flatMap((g) => g.links).find((l) => href({ project: l.project, screen: l.name }) === path);
  return (
    <div className={s.system} data-system>
      <UnstyledButton className={s.systemHead} aria-expanded={open} data-active={here && !open ? true : undefined} data-system-head
        onClick={() => setOpen(!open)}>
        <NamedIcon name={here?.icon ?? 'layout-grid'} fallback={here ? here.title : t('nav.control')} size={16} />
        <span className={s.linkTitle}>{here ? here.title : t('nav.control')}</span>
        {menu.master && <NavStatus m={{ ...menu.master, running: 0 }} />}
        <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />
      </UnstyledButton>
      <Collapse expanded={open}>
        {groups.map((g) => (
          <div key={g.key} className={s.systemGroup} data-group={g.key === 'work' ? '' : g.key} aria-label={g.key === 'master' ? name : g.title}>
            {g.links.map((l) => {
              const to = href({ project: l.project, screen: l.name });
              return (
                <a key={l.name} href={to} className={s.footLink} onClick={onPick} data-active={to === path || undefined}>
                  <NamedIcon name={l.icon ?? 'point'} fallback={l.title} size={16} />
                  <span className={s.linkTitle}>{l.title}</span>
                </a>
              );
            })}
          </div>
        ))}
      </Collapse>
    </div>
  );
}

// Nav lists the projects of the side, each a group that folds, or with one screen a header that opens it, and the
// groups of deck and the master below them; a move to a screen unfolds its group.
export const Nav = memo(function Nav({ menu, current, onPick }: { menu: MenuData; current: string; onPick?: () => void }) {
  const [folded, setFolded] = useLocalStorage<string[]>({ key: 'deck-nav-folded', defaultValue: [] });
  const here = menu.projects.find((m) => [...m.screens, ...(m.extra ?? [])].some((x) => href({ project: m.project, screen: x.name }) === current));
  const hereKey = here ? here.project ?? '' : undefined;
  useEffect(() => {
    if (hereKey !== undefined && folded.includes(hereKey)) setFolded(folded.filter((x) => x !== hereKey));
  }, [current, hereKey]);
  return (
    <>
      <ScrollArea className={s.navList} scrollbarSize={6} type="hover">
        {menu.projects.map((m) => {
          const key = m.project ?? '';
          const open = !folded.includes(key) || m.screens.length === 0;
          const head = (
            <>
              <span className={s.groupIcon}><NamedIcon name={m.icon} fallback={m.title} size={16} /></span>
              <span className={s.groupTitle}>{m.title}</span>
              <NavStatus m={m} />
            </>
          );
          const to = m.open && href({ project: m.project, screen: m.open.name });
          return (
            <div key={key} className={s.group} data-group={key} data-single={to ? true : undefined}>
              {to ? (
                <a href={to} className={s.groupHead} data-group-head data-active={to === current || undefined} onClick={onPick}>{head}</a>
              ) : (
                <UnstyledButton className={s.groupHead} aria-expanded={open} data-group-head
                  onClick={() => setFolded(open ? [...folded.filter((x) => x !== key), key] : folded.filter((x) => x !== key))}>
                  {head}
                  {m.screens.length > 0 && <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />}
                </UnstyledButton>
              )}
              {!!m.extra?.length && (
                <span className={s.extras}>
                  {m.extra.map((x) => {
                    const at = href({ project: m.project, screen: x.name });
                    return (
                      <Tooltip key={x.name} label={x.title}>
                        <a href={at} className={s.extra} data-active={at === current || undefined} aria-label={x.title} data-extra={x.name} onClick={onPick}>
                          <NamedIcon name={x.icon} fallback={x.title} size={15} />
                        </a>
                      </Tooltip>
                    );
                  })}
                </span>
              )}
              {!to && (
                <Collapse expanded={open}>
                  {m.screens.map((x) => {
                    const at = href({ project: m.project, screen: x.name });
                    return (
                      <a key={x.name} href={at} className={s.link} data-active={at === current || undefined} onClick={onPick}>
                        <NamedIcon name={x.icon ?? 'point'} fallback={x.title} size={17} />
                        <span className={s.linkTitle}>{x.title}</span>
                      </a>
                    );
                  })}
                </Collapse>
              )}
            </div>
          );
        })}
      </ScrollArea>
      {menu.system.length > 0 && <div className={s.foot} data-nav-foot><System menu={menu} current={current} onPick={onPick} /></div>}
    </>
  );
});
