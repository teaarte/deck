import { useEffect, useState } from 'react';
import { Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { openTopic, setChatScreen } from '../chat/chatStore';
import { useFocusHandoff } from '../hooks/useFocusHandoff';
import { entryOf, MenuContext, useMenu } from '../hooks/useMenu';
import { usePhone } from '../hooks/usePhone';
import { setLanguage } from '../lib/i18n';
import { Apps } from '../apps/Apps';
import { preload } from '../apps/covers';
import { ScreenLead } from '../screen/lead';
import { ScreenView } from '../screen/ScreenView';
import { PressAsks } from '../ui/actions/PressAsks';
import s from './shell.module.css';
import { Backdrop } from './Backdrop';
import { ChatPanel } from './ChatPanel';
import { MasterButton } from './MasterButton';
import { ModuleLead } from './ModuleLead';
import { PhoneTabs } from './PhoneTabs';
import { Places } from './Places';
import { BackButton, Toolbar } from './Toolbar';
import { dropParam, onMove, useRoute } from './router';
import { titleCount, titlePlace } from './title';
import { useChatLayout } from './useChatLayout';

// Shell is the frame of deck: the tiles or a screen under the tabs of its screens, and the chat with the master beside
// it; on a phone the tabs of deck at its foot.
export function Shell() {
  const { t } = useTranslation();
  const route = useRoute();
  const menu = useMenu();
  const asks = menu.pulse.asks;
  useEffect(() => {
    if (!('setAppBadge' in navigator)) return;
    (asks > 0 ? navigator.setAppBadge(asks) : navigator.clearAppBadge()).catch(() => undefined);
  }, [asks]);
  useEffect(() => titleCount(menu.pulse.notes ?? 0), [menu.pulse.notes]);
  useEffect(() => { if (menu.language) setLanguage(menu.language); }, [menu.language]);
  useEffect(() => preload(menu), [menu]);
  const layout = useChatLayout();
  const { chat, setChat, full } = layout;
  const phone = usePhone();
  const [dragging, setDragging] = useState(false);
  const apps = !route.project && route.screen === 'apps';
  const moveFocus = useFocusHandoff([chat]);
  useEffect(() => setChatScreen(route), [route.project, route.screen, route.record]);
  useEffect(() => {
    if (route.params?.chat !== 'new') return;
    dropParam('chat');
    openTopic('new');
  }, [route.params?.chat]);
  // A screen the owner picks shows: the chat over it, the phone's sheet or the full width, steps aside.
  useEffect(() => onMove(() => {
    if (chat && phone) setChat(false);
    else if (chat && full) layout.setFull(false);
  }), [chat, phone, full]);
  const entry = entryOf(menu, route.project);
  const screen = apps ? t('nav.apps') : route.screen === 'cockpit' && !route.project ? t('nav.cockpit') : [...(entry?.screens ?? []), ...(entry?.extra ?? [])].find((x) => x.name === route.screen)?.title;
  useEffect(() => titlePlace([screen, route.project && entry?.title].filter(Boolean).join(' · ')), [screen, route.project, entry?.title]);
  const toggle = () => { moveFocus(chat ? '[data-chat-toggle]' : '[data-chat-close]'); setChat(!chat); };
  const side = !phone && chat;
  const heading = apps ? t('nav.deck') : route.project ? entry?.title : undefined;
  return (
    <MenuContext.Provider value={menu}>
      <Backdrop />
      <div className={s.shell} data-full={(side && full) || undefined} data-phone={phone || undefined} data-dragging={dragging || undefined}
        style={!phone ? { gridTemplateColumns: [!(side && full) && 'minmax(0, 1fr)', side && (full ? 'minmax(0, 1fr)' : 'auto')].filter(Boolean).join(' ') } : undefined}>
        <main className={s.main} data-main data-main-apps={apps || undefined}>
          {phone ? (
            (heading || route.record) && (
              <header className={s.phoneHead} data-phone-head>
                <BackButton />
                {heading && <Title order={1} className={s.phoneTitle} data-phone-title>{heading}</Title>}
              </header>
            )
          ) : <Toolbar master={!chat && <MasterButton m={menu.master} open={chat} onClick={toggle} />} />}
          {!apps && <Places />}
          {apps ? <Apps /> : (
            <ScreenLead.Provider value={route.project && entry && !route.record ? <ModuleLead entry={entry} /> : null}>
              <ScreenView route={route} />
            </ScreenLead.Provider>
          )}
        </main>
        <ChatPanel phone={phone} layout={layout} onDragging={setDragging}
          onClose={() => { moveFocus('[data-chat-toggle]'); setChat(false); }} />
      </div>
      {phone && <PhoneTabs onChat={toggle} />}
      <PressAsks />
    </MenuContext.Provider>
  );
}
