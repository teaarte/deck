import { useCallback, useEffect, useRef, useState } from 'react';
import { ActionIcon, Badge, Button, Collapse, Drawer, Group, ScrollArea, Tooltip, UnstyledButton } from '@mantine/core';
import { useLocalStorage, useMediaQuery } from '@mantine/hooks';
import { IconArrowLeft, IconArrowsMaximize, IconArrowsMinimize, IconChevronRight, IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand, IconMenu2, IconSparkles, IconX } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { call, events } from './api';
import { ScreenView } from './screen';
import { back, href, useCanGoBack, useRoute, type Route } from './router';
import { setChatScreen } from './chat';
import { NamedIcon } from './icons';
import { Backdrop } from './backdrop';
import s from './shell.module.css';

export interface MenuEntry {
  project?: string;
  title: string;
  icon?: string;
  screens: { name: string; title: string; icon?: string }[];
  extra?: { name: string; title: string; icon?: string }[];
  running?: number;
  asks?: number;
  errors?: number;
}

const CHAT: Route = { screen: 'chat' };
const PULSE = ['processes', 'asks', 'health', 'projects'];
const MIN_CHAT = 320;

// Status shows what a project is busy with: running processes, questions to the owner, failing health.
function Status({ m }: { m: MenuEntry }) {
  const { t } = useTranslation();
  return (
    <Group gap={6} wrap="nowrap" data-status>
      {!!m.errors && <Tooltip label={t('nav.errors', { n: m.errors })}><span className={s.dot} role="img" aria-label={t('nav.errors', { n: m.errors })} data-errors={m.errors} /></Tooltip>}
      {!!m.asks && <Tooltip label={t('nav.asks', { n: m.asks })}><Badge size="sm" circle color="orange" aria-label={t('nav.asks', { n: m.asks })} data-asks={m.asks}>{m.asks}</Badge></Tooltip>}
      {!!m.running && <Tooltip label={t('nav.running', { n: m.running })}><span className={s.pulse} role="img" aria-label={t('nav.running', { n: m.running })} data-running={m.running} /></Tooltip>}
    </Group>
  );
}

// Nav lists deck's screens and every project's, each project a group that folds; a move to a screen unfolds its group.
function Nav({ menu, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const { t } = useTranslation();
  const [folded, setFolded] = useLocalStorage<string[]>({ key: 'deck-nav-folded', defaultValue: [] });
  const here = menu.find((m) => [...m.screens, ...(m.extra ?? [])].some((x) => href({ project: m.project, screen: x.name }) === current));
  const hereKey = here ? here.project ?? '' : undefined;
  useEffect(() => {
    if (hereKey !== undefined && folded.includes(hereKey)) setFolded(folded.filter((x) => x !== hereKey));
  }, [current, hereKey]);
  return (
    <ScrollArea className={s.navList} scrollbarSize={6} type="hover">
      {menu.map((m) => {
        const key = m.project ?? '';
        const open = !folded.includes(key) || m.screens.length === 0;
        return (
          <div key={key} className={s.group} data-group={key}>
            <UnstyledButton className={s.groupHead} aria-expanded={open} data-group-head
              onClick={() => setFolded(open ? [...folded.filter((x) => x !== key), key] : folded.filter((x) => x !== key))}>
              <span className={s.groupIcon}><NamedIcon name={m.icon ?? (m.project ? undefined : 'layout-grid')} fallback={m.title} size={16} /></span>
              <span className={s.groupTitle}>{m.project ? m.title : t('nav.deck')}</span>
              <Status m={m} />
              <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />
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
  );
}

const GLYPHS = ['✦', '✧', '✶', '✷', '✸', '✹', '✺', '✹', '✸', '✷', '✶', '✧'];

// Glyph is the mark of the master: still when it rests, a cycle of stars while it works.
function Glyph({ busy }: { busy: boolean }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (!busy || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const timer = setInterval(() => setI((n) => (n + 1) % GLYPHS.length), 110);
    return () => clearInterval(timer);
  }, [busy]);
  if (!busy) return <IconSparkles size={14} />;
  return <span className={s.glyph} aria-hidden>{GLYPHS[i]}</span>;
}

// MasterButton opens the chat; it shows whether the master works, waits for the owner or failed.
function MasterButton({ m, open, onClick }: { m?: MenuEntry; open: boolean; onClick: () => void }) {
  const { t } = useTranslation();
  const busy = !!m?.running;
  const state = busy ? t('chat.working') : m?.asks ? t('nav.asks', { n: m.asks }) : m?.errors ? t('nav.errors', { n: m.errors }) : undefined;
  return (
    <Tooltip label={state ?? t('chat.master')} disabled={!state}>
      <Button size="xs" radius="xl" variant="filled" className={s.master} data-busy={busy || undefined} data-open={open || undefined}
        leftSection={<Glyph busy={busy} />} onClick={onClick} aria-label={state ? `${t('chat.master')}: ${state}` : undefined} data-chat-toggle
        rightSection={m?.asks ? <span className={s.masterAsks} data-master-asks>{m.asks}</span> : m?.errors ? <span className={s.dot} /> : undefined}>
        {t('chat.master')}
      </Button>
    </Tooltip>
  );
}

function Logo() {
  return (
    <span className={s.logo}>
      <span className={s.logoMark}><IconSparkles size={16} stroke={2} /></span>
      deck
    </span>
  );
}

// useGrip resizes the chat panel by a drag of its left edge.
function useGrip(width: number, setWidth: (w: number) => void) {
  const [dragging, setDragging] = useState(false);
  const from = useRef({ x: 0, w: 0 });
  const down = (e: React.PointerEvent) => {
    from.current = { x: e.clientX, w: width };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    setDragging(true);
  };
  const move = (e: React.PointerEvent) => {
    if (!dragging) return;
    const max = Math.max(MIN_CHAT, window.innerWidth * 0.7);
    setWidth(Math.round(Math.min(max, Math.max(MIN_CHAT, from.current.w + from.current.x - e.clientX))));
  };
  const up = () => setDragging(false);
  const key = (e: React.KeyboardEvent) => {
    const step = e.key === 'ArrowLeft' ? 24 : e.key === 'ArrowRight' ? -24 : 0;
    if (!step) return;
    e.preventDefault();
    setWidth(Math.min(Math.max(MIN_CHAT, window.innerWidth * 0.7), Math.max(MIN_CHAT, width + step)));
  };
  return { dragging, props: { onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerCancel: up, onKeyDown: key,
    tabIndex: 0, 'aria-valuenow': width, 'aria-valuemin': MIN_CHAT } };
}

// Shell is the frame of deck: the menu, the screen, and the chat with the master, each its own island.
export function Shell() {
  const { t } = useTranslation();
  const route = useRoute();
  const canBack = useCanGoBack();
  const [menu, setMenu] = useState<MenuEntry[]>([]);
  const loadMenu = useCallback(() => { call<MenuEntry[]>('deck.menu').then(setMenu, () => setMenu([])); }, []);
  const [phoneNav, setPhoneNav] = useState(false);
  useEffect(() => {
    loadMenu();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let down = false;
    const stop = events((topic) => {
      if (!PULSE.includes(topic)) return;
      clearTimeout(timer);
      timer = setTimeout(loadMenu, 400);
    }, (live) => {
      if (live && down) loadMenu();
      down = !live;
    });
    const shown = () => { if (!document.hidden) loadMenu(); };
    document.addEventListener('visibilitychange', shown);
    return () => { document.removeEventListener('visibilitychange', shown); clearTimeout(timer); stop(); };
  }, []);
  const current = href({ ...route, record: undefined });
  const [chat, setChat] = useLocalStorage({ key: 'deck-chat-open', defaultValue: false });
  const [full, setFull] = useLocalStorage({ key: 'deck-chat-full', defaultValue: false });
  const [width, setWidth] = useLocalStorage({ key: 'deck-chat-width', defaultValue: 420 });
  const [navOpen, setNavOpen] = useLocalStorage({ key: 'deck-nav-open', defaultValue: true });
  const phone = useMediaQuery('(max-width: 48em)', window.matchMedia('(max-width: 48em)').matches, { getInitialValueInEffect: false });
  const grip = useGrip(width, setWidth);
  // A button that hides itself hands the focus to the one that brings its part back.
  const focusTo = useRef('');
  const moveFocus = (to: string) => { focusTo.current = to; };
  useEffect(() => {
    if (!focusTo.current) return;
    document.querySelector<HTMLElement>(focusTo.current)?.focus();
    focusTo.current = '';
  }, [navOpen, chat]);
  useEffect(() => { setChatScreen(route); setPhoneNav(false); }, [route]);
  useEffect(() => {
    const open = () => setChat(true);
    window.addEventListener('deck-chat-open', open);
    return () => window.removeEventListener('deck-chat-open', open);
  }, []);
  const entry = menu.find((m) => (m.project ?? '') === (route.project ?? ''));
  const panel = <ScreenView route={CHAT} onProjects={loadMenu} />;
  const master = (
    <MasterButton m={menu.find((x) => x.project === 'master')} open={chat}
      onClick={() => { moveFocus(chat ? '[data-chat-toggle]' : '[data-chat-close]'); setChat(!chat); }} />
  );
  const backButton = (
    <Tooltip label={t('nav.back')}>
      <ActionIcon variant="default" radius="xl" size="lg" onClick={back} aria-label={t('nav.back')} data-back>
        <IconArrowLeft size={18} />
      </ActionIcon>
    </Tooltip>
  );
  const crumb = entry?.project && (
    <span className={s.crumb}><NamedIcon name={entry.icon} fallback={entry.title} size={16} />{entry.title}</span>
  );

  const desk = !phone;
  const side = desk && chat;
  return (
    <>
      <Backdrop />
      <div className={s.shell} data-full={(side && full) || undefined} data-dragging={grip.dragging || undefined}
        style={desk ? { gridTemplateColumns: [navOpen && 'auto', !(side && full) && 'minmax(0, 1fr)', side && (full ? 'minmax(0, 1fr)' : 'auto')].filter(Boolean).join(' ') } : undefined}>
        {desk && navOpen ? (
          <nav className={`${s.island} ${s.nav}`} data-nav>
            <div className={s.navTop}>
              <Logo />
              <Tooltip label={t('nav.hide')}>
                <ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => { moveFocus('[data-nav-show]'); setNavOpen(false); }} aria-label={t('nav.hide')} data-nav-hide>
                  <IconLayoutSidebarLeftCollapse size={18} />
                </ActionIcon>
              </Tooltip>
            </div>
            <Nav menu={menu} current={current} />
          </nav>
        ) : null}
        <main className={s.main} data-main>
          {phone ? (
            <header className={s.phoneBar} data-phone-bar>
              <Group gap={6} wrap="nowrap" miw={0}>
                <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" onClick={() => setPhoneNav(true)} aria-label={t('nav.menu')} data-nav-open>
                  <IconMenu2 size={20} />
                </ActionIcon>
                {canBack && backButton}
                {crumb || <Logo />}
              </Group>
              {master}
            </header>
          ) : (
            <div className={s.toolbar}>
              <Group gap="xs" wrap="nowrap" miw={0}>
                {!navOpen && (
                  <Tooltip label={t('nav.show')}>
                    <ActionIcon variant="default" radius="xl" size="lg" onClick={() => { moveFocus('[data-nav-hide]'); setNavOpen(true); }} aria-label={t('nav.show')} data-nav-show>
                      <IconLayoutSidebarLeftExpand size={18} />
                    </ActionIcon>
                  </Tooltip>
                )}
                {canBack && backButton}
                {crumb}
              </Group>
              {!chat && master}
            </div>
          )}
          <ScreenView route={route} onProjects={loadMenu} />
        </main>
        {side ? (
          <aside className={`${s.island} ${s.aside}`} style={{ width: `min(${width}px, calc(100vw - ${navOpen ? 276 : 0}px - 440px))` }} data-chat-panel>
            <div className={s.grip} {...grip.props} data-dragging={grip.dragging || undefined} role="separator" aria-orientation="vertical" aria-label={t('chat.resize')} />
            <div className={s.asideHead}>
              <Group gap={8}><span className={s.logoMark}><IconSparkles size={15} stroke={2} /></span>{t('chat.master')}</Group>
              <Group gap={2}>
                <Tooltip label={full ? t('chat.exit_full') : t('chat.full')}>
                  <ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => setFull(!full)} aria-label={full ? t('chat.exit_full') : t('chat.full')} data-chat-full>
                    {full ? <IconArrowsMinimize size={17} /> : <IconArrowsMaximize size={17} />}
                  </ActionIcon>
                </Tooltip>
                <Tooltip label={t('chat.close')}>
                  <ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => { moveFocus('[data-chat-toggle]'); setChat(false); }} aria-label={t('chat.close')} data-chat-close>
                    <IconX size={17} />
                  </ActionIcon>
                </Tooltip>
              </Group>
            </div>
            <div className={`${s.asideBody} ${s.fill}`}>{panel}</div>
          </aside>
        ) : null}
      </div>
      <Drawer opened={phone && phoneNav} onClose={() => setPhoneNav(false)} size={300} padding={0} withCloseButton={false} keepMounted data-nav-phone>
        <nav className={`${s.island} ${s.safe}`} style={{ height: '100dvh', borderRadius: 0, boxShadow: 'none', background: 'transparent' }}>
          <div className={s.navTop}>
            <Logo />
            <ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => setPhoneNav(false)} aria-label={t('nav.hide')} data-autofocus>
              <IconX size={18} />
            </ActionIcon>
          </div>
          <Nav menu={menu} current={current} onPick={() => setPhoneNav(false)} />
        </nav>
      </Drawer>
      <Drawer opened={phone && chat} onClose={() => setChat(false)} size="100%" position="right" withCloseButton={false} padding="xs" data-chat-phone
        styles={{ body: { height: '100%', display: 'flex', flexDirection: 'column' } }}>
        <Group justify="space-between" px={6}><Logo /><ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => setChat(false)} aria-label={t('chat.close')}><IconX size={18} /></ActionIcon></Group>
        <div className={s.fill} style={{ flex: 1, minHeight: 0 }}>{phone && chat && panel}</div>
      </Drawer>
    </>
  );
}
