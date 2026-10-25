import { ActionIcon, Drawer, Group, Text, Tooltip } from '@mantine/core';
import { IconActivity, IconArrowsMaximize, IconArrowsMinimize, IconSparkles, IconX } from '@tabler/icons-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useTurnLive } from '../chat/chatStore';
import { useResizable } from '../hooks/useResizable';
import { ScreenView } from '../screen/ScreenView';
import { DekiHead, useMascot, useMasterName } from '../ui/deki/Deki';
import s from './shell.module.css';
import type { useChatLayout } from './useChatLayout';
import type { Route } from './router';

const CHAT: Route = { screen: 'chat' };

type Layout = ReturnType<typeof useChatLayout>;

// ChatPanel is the chat with the master: an island beside the screen that its left edge resizes, or on a phone a sheet
// over it.
export function ChatPanel({ phone, layout, onClose, onDragging }: {
  phone: boolean; layout: Layout; onClose: () => void; onDragging: (on: boolean) => void;
}) {
  const { t } = useTranslation();
  const grip = useResizable({ min: 280, maxRatio: 0.7, value: layout.width, onCommit: layout.setWidth, onDragging });
  const mascot = useMascot();
  const name = useMasterName();
  const live = useTurnLive();
  // Once opened the chat stays drawn while closed, so it opens again as it was; closed, it reads what changed on opening.
  const [opened, setOpened] = useState(layout.chat);
  useEffect(() => { if (layout.chat) setOpened(true); }, [layout.chat]);
  const panel = opened ? <ScreenView route={CHAT} visible={layout.chat} /> : null;
  const mark = mascot ? <DekiHead size={30} square /> : <span className={s.mark}><IconSparkles size={15} stroke={2} /></span>;
  const title = (
    <Group gap={8} wrap="nowrap" miw={0} fw={700}>
      {mark}<span className={s.asideTitle}>{name}</span>
      {live && <Group gap={3} wrap="nowrap" c="var(--mantine-color-green-text)" data-chat-live><IconActivity size={14} /><Text size="xs" fw={600} c="inherit">{t('chat.live')}</Text></Group>}
    </Group>
  );
  if (phone) {
    return (
      <Drawer opened={layout.chat} onClose={() => layout.setChat(false)} size="100%" position="right" withCloseButton={false} padding="xs" data-chat-phone keepMounted
        styles={{ body: { height: '100%', display: 'flex', flexDirection: 'column' } }}>
        <Group justify="space-between" wrap="nowrap" px={6}>{title}<ActionIcon variant="subtle" color="gray" onClick={() => layout.setChat(false)} aria-label={t('chat.close')} data-chat-close><IconX size={18} /></ActionIcon></Group>
        <div className={s.fill} style={{ flex: 1, minHeight: 0 }}>{panel}</div>
      </Drawer>
    );
  }
  if (!opened) return null;
  const full = layout.full;
  return (
    <aside className={`${s.island} ${s.aside}`} style={{ width: `min(${grip.width}px, calc(100vw - 440px))`, display: layout.chat ? undefined : 'none' }}
      data-chat-panel={layout.chat || undefined} data-island>
      <div className={s.grip} {...grip.props} data-dragging={grip.dragging || undefined} role="separator" aria-orientation="vertical" aria-label={t('chat.resize')} />
      <div className={s.asideHead}>
        {title}
        <Group gap={0} wrap="nowrap">
          <Tooltip label={full ? t('chat.exit_full') : t('chat.full')}>
            <ActionIcon variant="subtle" color="gray" size={28} onClick={() => layout.setFull(!full)} aria-label={full ? t('chat.exit_full') : t('chat.full')} data-chat-full>
              {full ? <IconArrowsMinimize size={15} /> : <IconArrowsMaximize size={15} />}
            </ActionIcon>
          </Tooltip>
          <Tooltip label={t('chat.close')}>
            <ActionIcon variant="subtle" color="gray" size={28} onClick={onClose} aria-label={t('chat.close')} data-chat-close>
              <IconX size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </div>
      <div className={`${s.asideBody} ${s.fill}`}>{panel}</div>
    </aside>
  );
}
