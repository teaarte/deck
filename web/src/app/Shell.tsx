import { useEffect, useState } from 'react';
import { ActionIcon, Drawer, Group, Tooltip } from '@mantine/core';
import { IconLayoutSidebarLeftCollapse, IconX } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { setChatScreen } from '../chat/chatStore';
import { useFocusHandoff } from '../hooks/useFocusHandoff';
import { useLanguageSetting } from '../hooks/useLanguageSetting';
import { useMenu } from '../hooks/useMenu';
import { usePhone } from '../hooks/usePhone';
import { ScreenView } from '../screen/ScreenView';
import s from './shell.module.css';
import { Backdrop } from './Backdrop';
import { Bell } from './Bell';
import { ChatPanel } from './ChatPanel';
import { Logo } from './Logo';
import { MasterButton } from './MasterButton';
import { Nav } from './Nav';
import { PhoneBar } from './PhoneBar';
import { Toolbar } from './Toolbar';
import { href, useRoute } from './router';
import { useChatLayout } from './useChatLayout';

// Shell is the frame of deck: the menu, the screen, and the chat with the master, each its own island.
export function Shell() {
  const { t } = useTranslation();
  const route = useRoute();
  const { menu, reload } = useMenu();
  useLanguageSetting();
  const layout = useChatLayout();
  const { chat, setChat, full, navOpen, setNavOpen } = layout;
  const phone = usePhone();
  const [phoneNav, setPhoneNav] = useState(false);
  const [dragging, setDragging] = useState(false);
  const moveFocus = useFocusHandoff([navOpen, chat]);
  useEffect(() => setChatScreen(route), [route.project, route.screen, route.record]);
  useEffect(() => setPhoneNav(false), [route]);
  const current = href({ ...route, record: undefined });
  const entry = menu.find((m) => (m.project ?? '') === (route.project ?? ''));
  const master = (
    <MasterButton m={menu.find((x) => x.project === 'master')} open={chat}
      onClick={() => { moveFocus(chat ? '[data-chat-toggle]' : '[data-chat-close]'); setChat(!chat); }} />
  );
  const desk = !phone;
  const side = desk && chat;
  return (
    <>
      <Backdrop />
      <div className={s.shell} data-full={(side && full) || undefined} data-dragging={dragging || undefined}
        style={desk ? { gridTemplateColumns: [navOpen && 'auto', !(side && full) && 'minmax(0, 1fr)', side && (full ? 'minmax(0, 1fr)' : 'auto')].filter(Boolean).join(' ') } : undefined}>
        {desk && navOpen ? (
          <nav className={`${s.island} ${s.nav}`} data-nav>
            <div className={s.navTop}>
              <Logo />
              <Group gap={2}>
                <Bell />
                <Tooltip label={t('nav.hide')}>
                  <ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => { moveFocus('[data-nav-show]'); setNavOpen(false); }} aria-label={t('nav.hide')} data-nav-hide>
                    <IconLayoutSidebarLeftCollapse size={18} />
                  </ActionIcon>
                </Tooltip>
              </Group>
            </div>
            <Nav menu={menu} current={current} />
          </nav>
        ) : null}
        <main className={s.main} data-main>
          {phone ? <PhoneBar entry={entry} onMenu={() => setPhoneNav(true)} master={master} />
            : <Toolbar entry={entry} navOpen={navOpen} onShowNav={() => { moveFocus('[data-nav-hide]'); setNavOpen(true); }} master={!chat && master} />}
          <ScreenView route={route} onProjects={reload} />
        </main>
        <ChatPanel phone={phone} layout={layout} onProjects={reload} onDragging={setDragging}
          onClose={() => { moveFocus('[data-chat-toggle]'); setChat(false); }} />
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
    </>
  );
}
