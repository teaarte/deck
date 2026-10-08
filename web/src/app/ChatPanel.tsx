import { ActionIcon, Drawer, Group, Tooltip } from '@mantine/core';
import { IconArrowsMaximize, IconArrowsMinimize, IconSparkles, IconX } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useResizable } from '../hooks/useResizable';
import { ScreenView } from '../screen/ScreenView';
import { DekiHead, useMascot, useMasterName } from '../ui/Deki';
import s from './shell.module.css';
import type { useChatLayout } from './useChatLayout';
import type { Route } from './router';

const CHAT: Route = { screen: 'chat' };

type Layout = ReturnType<typeof useChatLayout>;

// ChatPanel is the chat with the master: an island beside the screen that its left edge resizes, or on a phone a sheet
// over it.
export function ChatPanel({ phone, layout, onProjects, onClose, onDragging }: {
  phone: boolean; layout: Layout; onProjects: () => void; onClose: () => void; onDragging: (on: boolean) => void;
}) {
  const { t } = useTranslation();
  const grip = useResizable({ min: 280, maxRatio: 0.7, value: layout.width, onCommit: layout.setWidth, onDragging });
  const mascot = useMascot();
  const name = useMasterName();
  const panel = <ScreenView route={CHAT} onProjects={onProjects} />;
  const mark = mascot ? <DekiHead size={30} square /> : <span className={s.logoMark}><IconSparkles size={15} stroke={2} /></span>;
  const title = <Group gap={8} wrap="nowrap" miw={0} fw={700}>{mark}<span className={s.asideTitle}>{name}</span></Group>;
  if (phone) {
    return (
      <Drawer opened={layout.chat} onClose={() => layout.setChat(false)} size="100%" position="right" withCloseButton={false} padding="xs" data-chat-phone
        styles={{ body: { height: '100%', display: 'flex', flexDirection: 'column' } }}>
        <Group justify="space-between" wrap="nowrap" px={6}>{title}<ActionIcon variant="subtle" color="gray" radius="xl" onClick={() => layout.setChat(false)} aria-label={t('chat.close')}><IconX size={18} /></ActionIcon></Group>
        <div className={s.fill} style={{ flex: 1, minHeight: 0 }}>{layout.chat && panel}</div>
      </Drawer>
    );
  }
  if (!layout.chat) return null;
  const full = layout.full;
  return (
    <aside className={`${s.island} ${s.aside}`} style={{ width: `min(${grip.width}px, calc(100vw - ${layout.navOpen ? 276 : 0}px - 440px))` }} data-chat-panel>
      <div className={s.grip} {...grip.props} data-dragging={grip.dragging || undefined} role="separator" aria-orientation="vertical" aria-label={t('chat.resize')} />
      <div className={s.asideHead}>
        {title}
        <Group gap={0} wrap="nowrap">
          <Tooltip label={full ? t('chat.exit_full') : t('chat.full')}>
            <ActionIcon variant="subtle" color="gray" radius="xl" size={28} onClick={() => layout.setFull(!full)} aria-label={full ? t('chat.exit_full') : t('chat.full')} data-chat-full>
              {full ? <IconArrowsMinimize size={15} /> : <IconArrowsMaximize size={15} />}
            </ActionIcon>
          </Tooltip>
          <Tooltip label={t('chat.close')}>
            <ActionIcon variant="subtle" color="gray" radius="xl" size={28} onClick={onClose} aria-label={t('chat.close')} data-chat-close>
              <IconX size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </div>
      <div className={`${s.asideBody} ${s.fill}`}>{panel}</div>
    </aside>
  );
}
