import { memo, useEffect } from 'react';
import { Collapse, Menu, ScrollArea, Tooltip, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconChevronRight, IconSelector } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { useMascot, useMasterName } from '../ui/Deki';
import { NamedIcon } from '../ui/NamedIcon';
import s from './nav.module.css';
import { NavStatus } from './NavStatus';
import { href } from './router';

const SYSTEM = 'master';

const links = (m: MenuEntry) => [...m.screens, ...(m.extra ?? [])];

type Link = { m: MenuEntry; x: MenuEntry['screens'][number]; title: string };

// System is deck's screens and the master's behind one button at the foot of the menu, apart from the projects: the
// work of deck, the settings of the master and deck's settings. The documents of the master are a tab of Files.
function System({ menu, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const { t } = useTranslation();
  const mascot = useMascot();
  const name = useMasterName();
  const path = current.split('?')[0];
  const deck = menu.find((m) => !m.project);
  const master = menu.find((m) => m.project === SYSTEM);
  const of = (m: MenuEntry | undefined, x: MenuEntry['screens'][number]): Link =>
    ({ m: m!, x, title: m?.project === SYSTEM && x.name === 'settings' ? t(mascot ? 'nav.deki_settings' : 'nav.master_settings') : x.title });
  const work = (deck?.screens ?? []).filter((x) => x.name !== 'settings').map((x) => of(deck, x));
  const own = master ? links(master).filter((x) => x.name !== 'documents').map((x) => of(master, x)) : [];
  const settings = (deck?.screens ?? []).filter((x) => x.name === 'settings').map((x) => of(deck, x));
  const groups = [{ key: 'work', label: t('nav.work'), links: work }, { key: SYSTEM, label: name, links: own }, { key: 'settings', label: '', links: settings }]
    .filter((g) => g.links.length);
  const here = groups.flatMap((g) => g.links).find((l) => href({ project: l.m.project, screen: l.x.name }) === path);
  return (
    <Menu position="top-start" width="target" withinPortal shadow="md">
      <Menu.Target>
        <UnstyledButton className={s.system} data-active={here ? true : undefined} data-system>
          <span className={s.groupIcon}><NamedIcon name={here?.x.icon ?? 'layout-grid'} fallback={here?.title ?? t('nav.control')} size={16} /></span>
          <span className={s.groupTitle}>{here ? here.title : t('nav.control')}</span>
          {master && <NavStatus m={master} />}
          <IconSelector size={14} className={s.chevron} />
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown data-system-menu>
        {groups.map((g, i) => (
          <div key={g.key} data-group={g.key === 'work' ? '' : g.key}>
            {i > 0 && <Menu.Divider />}
            {g.label && <Menu.Label>{g.label}</Menu.Label>}
            {g.links.map((l) => {
              const to = href({ project: l.m.project, screen: l.x.name });
              return (
                <Menu.Item key={l.x.name} component="a" href={to} onClick={onPick} data-active={to === path || undefined}
                  leftSection={<NamedIcon name={l.x.icon ?? 'point'} fallback={l.title} size={16} />}>
                  {l.title}
                </Menu.Item>
              );
            })}
          </div>
        ))}
      </Menu.Dropdown>
    </Menu>
  );
}

// Nav lists every project's screens, each project a group that folds, and deck's own with the master's below them;
// a move to a screen unfolds its group.
export const Nav = memo(function Nav({ menu: all, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const [folded, setFolded] = useLocalStorage<string[]>({ key: 'deck-nav-folded', defaultValue: [] });
  const system = all.filter((m) => !m.project || m.project === SYSTEM);
  const menu = all.filter((m) => m.project && m.project !== SYSTEM);
  const here = menu.find((m) => [...m.screens, ...(m.extra ?? [])].some((x) => href({ project: m.project, screen: x.name }) === current));
  const hereKey = here ? here.project ?? '' : undefined;
  useEffect(() => {
    if (hereKey !== undefined && folded.includes(hereKey)) setFolded(folded.filter((x) => x !== hereKey));
  }, [current, hereKey]);
  return (
    <>
      <ScrollArea className={s.navList} scrollbarSize={6} type="hover">
        {menu.map((m) => {
          const key = m.project ?? '';
          const open = !folded.includes(key) || m.screens.length === 0;
          return (
            <div key={key} className={s.group} data-group={key}>
              <UnstyledButton className={s.groupHead} aria-expanded={open} data-group-head
                onClick={() => setFolded(open ? [...folded.filter((x) => x !== key), key] : folded.filter((x) => x !== key))}>
                <span className={s.groupIcon}><NamedIcon name={m.icon} fallback={m.title} size={16} /></span>
                <span className={s.groupTitle}>{m.title}</span>
                <NavStatus m={m} />
                {m.screens.length > 0 && <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />}
              </UnstyledButton>
              {!!m.extra?.length && (
                <span className={s.extras}>
                  {m.extra.map((x) => {
                    const to = href({ project: m.project, screen: x.name });
                    return (
                      <Tooltip key={x.name} label={x.title}>
                        <a href={to} className={s.extra} data-active={to === current || undefined} aria-label={x.title} data-extra={x.name} onClick={onPick}>
                          <NamedIcon name={x.icon} fallback={x.title} size={15} />
                        </a>
                      </Tooltip>
                    );
                  })}
                </span>
              )}
              <Collapse expanded={open}>
                {m.screens.map((x) => {
                  const to = href({ project: m.project, screen: x.name });
                  return (
                    <a key={x.name} href={to} className={s.link} data-active={to === current || undefined} onClick={onPick}>
                      <NamedIcon name={x.icon ?? 'point'} fallback={x.title} size={17} />
                      <span className={s.linkTitle}>{x.title}</span>
                    </a>
                  );
                })}
              </Collapse>
            </div>
          );
        })}
      </ScrollArea>
      {system.length > 0 && <div className={s.foot} data-nav-foot><System menu={system} current={current} onPick={onPick} /></div>}
    </>
  );
});
