import { useEffect, useRef, useState } from 'react';
import { onVoiceWanted } from './chatStore';
import { ActionIcon, Anchor, Badge, Button, CloseButton, Group, Menu, Modal, Stack, Text, Textarea, Tooltip } from '@mantine/core';
import { IconArrowUp, IconFileMusic, IconMapPin, IconMessagePlus, IconMicrophone, IconMicrophoneFilled, IconPaperclip, IconPlayerStopFilled, IconPlus } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useRoute } from '../app/router';
import { entryOf, placesOf, useMenuData } from '../hooks/useMenu';
import s from './chat.module.css';
import type { useAttachments } from './useAttachments';
import { useVoice } from './useVoice';

// Composer is where the owner writes: the screen the message goes with, the files, the text, voice and send.
export function Composer({ text, setText, files, busy, onSend, onNewTopic, onEdit, onWalk }: {
  text: string; setText: (f: (prev: string) => string) => void; files: ReturnType<typeof useAttachments>; busy: boolean;
  onSend: (detached: boolean) => void; onNewTopic: () => void; onEdit: (text: string) => void;
  onWalk: (dir: 1 | -1, text: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const route = useRoute();
  const menu = useMenuData();
  // The screen the message goes with, by the titles the owner sees.
  const place = [route.project && (entryOf(menu, route.project)?.title ?? route.project),
    !route.project && route.screen === 'apps' ? t('nav.apps') : placesOf(menu, route.project).find((x) => x.name === route.screen)?.title ?? route.screen]
    .filter(Boolean).join(' · ');
  // detached sends the message without the screen the owner is on, so a new topic belongs to no module.
  const [detached, setDetached] = useState(false);
  useEffect(() => setDetached(false), [route.project, route.screen, route.record]);
  const picker = useRef<HTMLInputElement>(null);
  const mic = useRef<HTMLButtonElement>(null);
  useEffect(() => onVoiceWanted(() => mic.current?.focus()), []);
  const voice = useVoice((heard) => setText((prev) => (prev.trim() ? prev.trimEnd() + ' ' : '') + heard));
  const recording = useRef<HTMLInputElement>(null);
  const send = () => onSend(detached);
  return (
    <div className={s.composer} data-composer data-drop={files.dropping || undefined}
      onDragOver={(e) => { e.preventDefault(); files.setDropping(true); }} onDragLeave={() => files.setDropping(false)}
      onDrop={(e) => { e.preventDefault(); files.setDropping(false); files.attach(e.dataTransfer.files); }}>
      {(route.project || route.screen) && (
        <Group gap={6} px={6} pt={2}>
          {detached ? (
            <Badge size="sm" variant="outline" color="gray" tt="none" maw="100%" style={{ cursor: "pointer" }} onClick={() => setDetached(false)} data-context-off>
              {t('chat.no_context')}
            </Badge>
          ) : (
            <Badge size="sm" variant="light" tt="none" maw="100%" leftSection={<IconMapPin size={11} />} data-context
              rightSection={<CloseButton size="xs" onClick={() => setDetached(true)} aria-label={t('chat.drop_context')} />}>
              {place}
            </Badge>
          )}
        </Group>
      )}
      {files.files.length > 0 && (
        <Group gap={6} px={6} pt={4} data-attached>
          {files.files.map((f, i) => (
            <Badge key={i} size="lg" variant="light" maw="100%" leftSection={<IconPaperclip size={12} />}
              rightSection={<CloseButton size="xs" onClick={() => files.remove(i)} aria-label={t('chat.unattach')} />}>{f.name}</Badge>
          ))}
        </Group>
      )}
      {voice.failed && !voice.hearing && (
        <Group gap={6} px={6} pt={4} wrap="nowrap" data-voice-failed>
          <Text size="xs" c="red" style={{ flex: 1 }} lineClamp={2}>{t('chat.voice_kept', { error: voice.failed })}</Text>
          <Button size="compact-xs" variant="light" onClick={voice.again} data-voice-again>{t('chat.voice_again')}</Button>
          <Button size="compact-xs" variant="subtle" color="gray" onClick={voice.discard} data-voice-discard>{t('chat.voice_discard')}</Button>
        </Group>
      )}
      {voice.recording ? (
        <div className={s.hearing} data-hearing>
          <IconMicrophoneFilled size={18} color="var(--mantine-color-red-filled)" />
          <span>{t('chat.hearing')}</span>
        </div>
      ) : <Textarea variant="unstyled" autosize minRows={1} maxRows={10} placeholder={t('chat.placeholder')} value={text} data-chat-input px={8}
        onChange={(e) => onEdit(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); return; }
          if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && !e.shiftKey && !e.altKey && !e.metaKey && !e.ctrlKey) {
            const v = onWalk(e.key === 'ArrowUp' ? 1 : -1, text);
            if (v !== undefined) { e.preventDefault(); onEdit(v); }
          }
        }} />}
      <Group justify="space-between" gap={6}>
        <Menu position="top-start" shadow="md">
          <Menu.Target>
            <ActionIcon variant="subtle" color="gray" size="lg" aria-label={t('chat.add')} data-composer-add><IconPlus size={20} /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconPaperclip size={16} />} onClick={() => picker.current?.click()} data-attach>{t('chat.attach')}</Menu.Item>
            <Menu.Item leftSection={<IconFileMusic size={16} />} onClick={() => recording.current?.click()} data-voice-file>{t('chat.voice_file')}</Menu.Item>
            <Menu.Item leftSection={<IconMessagePlus size={16} />} onClick={onNewTopic}>{t('chat.new_topic')}</Menu.Item>
          </Menu.Dropdown>
        </Menu>
        <input ref={picker} type="file" multiple hidden onChange={(e) => { files.attach(e.currentTarget.files); e.currentTarget.value = ''; }} data-attach-input />
        <input ref={recording} type="file" accept="audio/*" hidden data-voice-input
          onChange={(e) => { const f = e.currentTarget.files?.[0]; e.currentTarget.value = ''; if (f) void voice.take(f); }} />
        <Modal opened={!!voice.noMic} onClose={voice.closeNoMic} title={t('chat.no_mic_title')} data-no-mic>
          <Stack gap="sm">
            <Text size="sm">{t('chat.no_mic')}</Text>
            {voice.noMic?.https
              ? <Anchor href={voice.noMic.https} size="sm" data-no-mic-https>{t('chat.no_mic_open', { url: voice.noMic.https })}</Anchor>
              : <Text size="sm" c="dimmed">{t('chat.no_mic_tailnet')}</Text>}
            <Group justify="flex-end">
              <Button variant="default" onClick={voice.closeNoMic}>{t('verb.cancel')}</Button>
              <Button leftSection={<IconFileMusic size={16} />} onClick={() => recording.current?.click()}>{t('chat.voice_file')}</Button>
            </Group>
          </Stack>
        </Modal>
        <Group gap={6}>
          {voice.recording ? (
            <Button size="xs" color="red" leftSection={<IconPlayerStopFilled size={12} />} onClick={voice.hear} data-mic-stop>{t('chat.stop_recording', { s: voice.recording - 1 })}</Button>
          ) : (
            <Tooltip label={t('chat.mic')}>
              <ActionIcon variant="subtle" color="gray" size="lg" loading={voice.hearing} onClick={() => void voice.listen()} aria-label={t('chat.mic')} data-mic ref={mic}><IconMicrophone size={19} /></ActionIcon>
            </Tooltip>
          )}
          <ActionIcon size="lg" variant="filled" loading={busy} disabled={!text.trim()} onClick={send} aria-label={t('verb.send')} data-chat-send>
            <IconArrowUp size={18} stroke={2.4} />
          </ActionIcon>
        </Group>
      </Group>
    </div>
  );
}
