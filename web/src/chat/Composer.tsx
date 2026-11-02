import type { Action } from '../lib/catalog.gen';
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

// Picture is a picture attached and not sent yet, shown from the device.
function Picture({ file }: { file: File }) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    const u = URL.createObjectURL(file);
    setUrl(u);
    return () => URL.revokeObjectURL(u);
  }, [file]);
  return url ? <img src={url} alt={file.name} title={file.name} /> : null;
}

// Composer is where the owner writes: the screen the message goes with, the files, the text, voice and send.
export function Composer({ text, setText, files, busy, transcribe, onSend, onNewTopic, onEdit, onWalk }: {
  text: string; setText: (f: (prev: string) => string) => void; files: ReturnType<typeof useAttachments>; busy: boolean; transcribe?: Action;
  onSend: (detached: boolean) => void; onNewTopic: () => void; onEdit: (text: string) => void;
  onWalk: (dir: 1 | -1, text: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const route = useRoute();
  const menu = useMenuData();
  // The screen the message goes with, by the titles the owner sees.
  const place = [route.project && (entryOf(menu, route.project)?.title ?? route.project),
    !route.project && route.screen === 'apps' ? t('Modules') : placesOf(menu, route.project).find((x) => x.name === route.screen)?.title ?? route.screen]
    .filter(Boolean).join(' · ');
  // detached sends the message without the screen the owner is on, so a new topic belongs to no module.
  const [detached, setDetached] = useState(false);
  useEffect(() => setDetached(false), [route.project, route.screen, route.record]);
  const picker = useRef<HTMLInputElement>(null);
  const mic = useRef<HTMLButtonElement>(null);
  useEffect(() => onVoiceWanted(() => mic.current?.focus()), []);
  const voice = useVoice((heard) => setText((prev) => (prev.trim() ? prev.trimEnd() + ' ' : '') + heard), transcribe);
  const recording = useRef<HTMLInputElement>(null);
  const send = () => onSend(detached);
  return (
    <div className={s.composer} data-composer data-drop={files.dropping || undefined}
      onDragOver={(e) => { e.preventDefault(); files.setDropping(true); }} onDragLeave={() => files.setDropping(false)}
      onDrop={(e) => { e.preventDefault(); files.setDropping(false); files.attach(e.dataTransfer.files); }}>
      {(route.project || route.screen) && (
        <Group gap={6} px={6} pt={2}>
          {detached ? (
            <Badge size="sm" variant="outline" color="gray" tt="none" maw="100%" className="deck-pointer" onClick={() => setDetached(false)} data-context-off>
              {t('Not about this screen')}
            </Badge>
          ) : (
            <Badge size="sm" variant="light" tt="none" maw="100%" leftSection={<IconMapPin size={11} />} data-context
              rightSection={<CloseButton size="xs" onClick={() => setDetached(true)} aria-label={t('Send without the screen')} />}>
              {place}
            </Badge>
          )}
        </Group>
      )}
      {files.files.length > 0 && (
        <Group gap={6} px={6} pt={4} data-attached>
          {files.files.map((f, i) => f.type.startsWith('image/') ? (
            <span key={i} className={s.attachedPicture} data-attached-picture>
              <Picture file={f} />
              <CloseButton size="xs" className={s.unattach} onClick={() => files.remove(i)} aria-label={t('Take the file away')} />
            </span>
          ) : (
            <Badge key={i} size="lg" variant="light" maw="100%" leftSection={<IconPaperclip size={12} />} data-attached-file
              rightSection={<CloseButton size="xs" onClick={() => files.remove(i)} aria-label={t('Take the file away')} />}>{f.name}</Badge>
          ))}
        </Group>
      )}
      {voice.failed && !voice.hearing && (
        <Group gap={6} px={6} pt={4} wrap="nowrap" data-voice-failed>
          <Text size="xs" c="red" className="deck-grow" lineClamp={2}>{t('The recording is kept, not turned into text: {{error}}', { error: voice.failed })}</Text>
          <Button size="compact-xs" variant="light" onClick={voice.again} data-voice-again>{t('Recognize again')}</Button>
          <Button size="compact-xs" variant="subtle" color="gray" onClick={voice.discard} data-voice-discard>{t('Discard')}</Button>
        </Group>
      )}
      {voice.recording ? (
        <div className={s.hearing} data-hearing>
          <IconMicrophoneFilled size={18} color="var(--mantine-color-red-filled)" />
          <span>{t('Speak — the recording becomes text')}</span>
        </div>
      ) : <Textarea variant="unstyled" autosize minRows={1} maxRows={10} placeholder={t('Message to the master')} value={text} data-chat-input px={8}
        onChange={(e) => onEdit(e.currentTarget.value)}
        onPaste={(e) => {
          // A copy from an app comes as rich text with a picture of it: the text is what was meant.
          if (!e.clipboardData.files.length || e.clipboardData.types.includes('text/html')) return;
          e.preventDefault();
          files.attach(e.clipboardData.files);
        }}
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
            <ActionIcon variant="subtle" color="gray" size="lg" aria-label={t('Add')} data-composer-add><IconPlus size={20} /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconPaperclip size={16} />} onClick={() => picker.current?.click()} data-attach>{t('Attach files')}</Menu.Item>
            <Menu.Item leftSection={<IconFileMusic size={16} />} onClick={() => recording.current?.click()} data-voice-file>{t('Voice from a file')}</Menu.Item>
            <Menu.Item leftSection={<IconMessagePlus size={16} />} onClick={onNewTopic}>{t('New topic')}</Menu.Item>
          </Menu.Dropdown>
        </Menu>
        <input ref={picker} type="file" multiple hidden onChange={(e) => { files.attach(e.currentTarget.files); e.currentTarget.value = ''; }} data-attach-input />
        <input ref={recording} type="file" accept="audio/*" hidden data-voice-input
          onChange={(e) => { const f = e.currentTarget.files?.[0]; e.currentTarget.value = ''; if (f) void voice.take(f); }} />
        <Modal opened={!!voice.noMic} onClose={voice.closeNoMic} title={t('No microphone')} data-no-mic>
          <Stack gap="sm">
            <Text size="sm">{t('A browser gives a microphone only to a page on https or on the same machine, and deck is open on http over the network.')}</Text>
            {voice.noMic?.https
              ? <Anchor href={voice.noMic.https} size="sm" data-no-mic-https>{t('Open deck on https: {{url}}', { url: voice.noMic.https })}</Anchor>
              : <Text size="sm" c="dimmed">{t('Start deck with -tailscale and open its https address, or upload a recording as a file.')}</Text>}
            <Group justify="flex-end">
              <Button variant="default" onClick={voice.closeNoMic}>{t('Cancel')}</Button>
              <Button leftSection={<IconFileMusic size={16} />} onClick={() => recording.current?.click()}>{t('Voice from a file')}</Button>
            </Group>
          </Stack>
        </Modal>
        <Group gap={6}>
          {voice.recording ? (
            <Button size="xs" color="red" leftSection={<IconPlayerStopFilled size={12} />} onClick={voice.hear} data-mic-stop>{t('Stop · {{s}} s', { s: voice.recording - 1 })}</Button>
          ) : (
            <Tooltip label={t('Speak')}>
              <ActionIcon variant="subtle" color="gray" size="lg" loading={voice.hearing} onClick={() => void voice.listen()} aria-label={t('Speak')} data-mic ref={mic}><IconMicrophone size={19} /></ActionIcon>
            </Tooltip>
          )}
          <ActionIcon size="lg" variant="filled" loading={busy} disabled={!text.trim()} onClick={send} aria-label={t('Send')} data-chat-send>
            <IconArrowUp size={18} stroke={2.4} />
          </ActionIcon>
        </Group>
      </Group>
    </div>
  );
}
