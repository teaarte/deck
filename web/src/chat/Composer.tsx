import { useEffect, useRef, useState } from 'react';
import { ActionIcon, Badge, Button, CloseButton, Group, Menu, Textarea, Tooltip } from '@mantine/core';
import { IconArrowUp, IconMapPin, IconMessagePlus, IconMicrophone, IconPaperclip, IconPlayerStopFilled, IconPlus } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useRoute } from '../router';
import s from '../shell.module.css';
import type { useAttachments } from './useAttachments';
import { useVoice } from './useVoice';

// Composer is where the owner writes: the screen the message goes with, the files, the text, voice and send.
export function Composer({ text, setText, files, busy, onSend, onNewTopic, onEdit }: {
  text: string; setText: (f: (prev: string) => string) => void; files: ReturnType<typeof useAttachments>; busy: boolean;
  onSend: (detached: boolean) => void; onNewTopic: () => void; onEdit: (text: string) => void;
}) {
  const { t } = useTranslation();
  const route = useRoute();
  // detached sends the message without the screen the owner is on, so a new topic belongs to no module.
  const [detached, setDetached] = useState(false);
  useEffect(() => setDetached(false), [route.project, route.screen, route.record]);
  const picker = useRef<HTMLInputElement>(null);
  const voice = useVoice((heard) => setText((prev) => (prev.trim() ? prev.trimEnd() + ' ' : '') + heard));
  const send = () => onSend(detached);
  return (
    <div className={s.composer} data-composer data-drop={files.dropping || undefined}
      onDragOver={(e) => { e.preventDefault(); files.setDropping(true); }} onDragLeave={() => files.setDropping(false)}
      onDrop={(e) => { e.preventDefault(); files.setDropping(false); files.attach(e.dataTransfer.files); }}>
      {(route.project || route.screen) && (
        <Group gap={6} px={6} pt={2}>
          {detached ? (
            <Badge size="sm" radius="sm" variant="outline" color="gray" tt="none" style={{ cursor: "pointer" }} onClick={() => setDetached(false)} data-context-off>
              {t('chat.no_context')}
            </Badge>
          ) : (
            <Badge size="sm" radius="sm" variant="light" tt="none" leftSection={<IconMapPin size={11} />} data-context
              rightSection={<CloseButton size="xs" onClick={() => setDetached(true)} aria-label={t('chat.drop_context')} />}>
              {[route.project, route.screen].filter(Boolean).join(' · ')}
            </Badge>
          )}
        </Group>
      )}
      {files.files.length > 0 && (
        <Group gap={6} px={6} pt={4} data-attached>
          {files.files.map((f, i) => (
            <Badge key={i} size="lg" radius="md" variant="light" leftSection={<IconPaperclip size={12} />}
              rightSection={<CloseButton size="xs" onClick={() => files.remove(i)} aria-label={t('chat.unattach')} />}>{f.name}</Badge>
          ))}
        </Group>
      )}
      <Textarea variant="unstyled" autosize minRows={1} maxRows={10} placeholder={t('chat.placeholder')} value={text} data-chat-input px={8}
        onChange={(e) => onEdit(e.currentTarget.value)}
        onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); } }} />
      <Group justify="space-between" gap={6}>
        <Menu position="top-start" shadow="md" radius="md">
          <Menu.Target>
            <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" aria-label={t('chat.add')} data-composer-add><IconPlus size={20} /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconPaperclip size={16} />} onClick={() => picker.current?.click()} data-attach>{t('chat.attach')}</Menu.Item>
            <Menu.Item leftSection={<IconMessagePlus size={16} />} onClick={onNewTopic}>{t('chat.new_topic')}</Menu.Item>
          </Menu.Dropdown>
        </Menu>
        <input ref={picker} type="file" multiple hidden onChange={(e) => { files.attach(e.currentTarget.files); e.currentTarget.value = ''; }} data-attach-input />
        <Group gap={6}>
          {voice.recording ? (
            <Button size="xs" color="red" radius="xl" leftSection={<IconPlayerStopFilled size={12} />} onClick={() => void voice.hear()} data-mic-stop>{t('chat.stop_recording', { s: voice.recording - 1 })}</Button>
          ) : (
            <Tooltip label={t('chat.mic')}>
              <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" loading={voice.hearing} onClick={() => void voice.listen()} aria-label={t('chat.mic')} data-mic><IconMicrophone size={19} /></ActionIcon>
            </Tooltip>
          )}
          <ActionIcon radius="xl" size="lg" variant="filled" loading={busy} disabled={!text.trim()} onClick={send} aria-label={t('verb.send')} data-chat-send>
            <IconArrowUp size={18} stroke={2.4} />
          </ActionIcon>
        </Group>
      </Group>
    </div>
  );
}
