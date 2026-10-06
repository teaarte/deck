import { useEffect, useRef, useState } from 'react';
import { ActionIcon, Badge, Button, Group, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core';
import { IconChevronLeft, IconChevronRight, IconFlag } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { token } from '../../lib/api';
import { timecode } from '../../lib/format';
import { replaceParam, useRoute } from '../../app/router';
import { ActionInput } from '../../ui/ActionInput';
import { useActionRun } from '../../ui/useActionRun';
import { Download } from './Download';
import type { Item } from './Media';
import s from './player.module.css';

// FRAME is the step of the frame buttons: one frame at 30 frames a second.
const FRAME = 1 / 30;

// finite is a duration the player knows, else zero.
const finite = (d?: number) => (d !== undefined && Number.isFinite(d) ? d : 0);

// Player plays a video or a sound of an item from its address, steps it by frames, shows the time as a timecode
// and the marks of the item on its timeline; its mark sends the second it is paused at to the command of the module.
export function Player({ it, first }: { it: Item; first: boolean }) {
  const { t } = useTranslation();
  const media = useRef<HTMLVideoElement & HTMLAudioElement>(null);
  const [now, setNow] = useState(0);
  const [length, setLength] = useState(0);
  const [marking, setMarking] = useState(false);
  const start = Number(useRoute().params?.t);
  const video = it.type?.startsWith('video/');
  const src = it.url + (it.url?.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(token());
  useEffect(() => {
    const m = media.current;
    if (m && m.readyState >= 1) setLength(finite(m.duration));
  }, [src]);
  useEffect(() => {
    const m = media.current;
    if (!m || !first || !Number.isFinite(start) || start <= 0) return;
    const seek = () => { m.currentTime = start; };
    if (m.readyState >= 1) seek();
    else m.addEventListener('loadedmetadata', seek, { once: true });
    return () => m.removeEventListener('loadedmetadata', seek);
  }, [first, start]);
  // The first player keeps the second it stopped at in t of the address, so blocks of the screen that read it follow.
  const keep = () => {
    const m = media.current;
    if (first && m && m.paused) replaceParam('t', String(Math.round(m.currentTime * 100) / 100));
  };
  const go = (at: number) => {
    const m = media.current;
    if (!m) return;
    m.pause();
    m.currentTime = Math.min(Math.max(at, 0), m.duration || at);
  };
  const mark = it.mark;
  const { busy, send: run } = useActionRun(mark ?? {}, { onDone: () => setMarking(false) });
  const send = (text: string) => {
    if (!mark?.command) return;
    void run({ file: it.file, at: Math.round(now * 100) / 100, ...(mark.input ? { [mark.input]: text } : {}) });
  };
  const props = {
    ref: media, src, controls: true, preload: 'metadata', className: s.media, 'data-player': video ? 'video' : 'audio',
    onTimeUpdate: () => setNow(media.current?.currentTime ?? 0), onSeeked: () => { setNow(media.current?.currentTime ?? 0); keep(); },
    onPause: keep,
    onLoadedMetadata: () => setLength(finite(media.current?.duration)), onDurationChange: () => setLength(finite(media.current?.duration)),
  };
  return (
    <Stack gap={6} className={s.player} data-item data-media={video ? 'video' : 'audio'}>
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm" fw={500} truncate>{it.title || it.name}</Text>
        <Download it={it} />
      </Group>
      <div className={s.stage}>
      <Stack gap={6} miw={0}>
      {video ? <video {...props} playsInline /> : <audio {...props} />}
      {length > 0 && !!it.marks?.length && (
        <div className={s.timeline} data-timeline>
          {it.marks.map((m, i) => (
            <Tooltip key={i} label={`${timecode(m.at)}${m.text ? ' · ' + m.text : ''}`}>
              <UnstyledButton className={s.tick} style={{ left: `${Math.min(m.at / length, 1) * 100}%` }} onClick={() => go(m.at)} data-tick={m.at} aria-label={timecode(m.at)} />
            </Tooltip>
          ))}
        </div>
      )}
      <Group gap={6} wrap="nowrap">
        <Tooltip label={t('player.back')}><ActionIcon variant="default" size="sm" onClick={() => go(now - FRAME)} data-frame="back" aria-label={t('player.back')}><IconChevronLeft size={14} /></ActionIcon></Tooltip>
        <Tooltip label={t('player.forward')}><ActionIcon variant="default" size="sm" onClick={() => go(now + FRAME)} data-frame="forward" aria-label={t('player.forward')}><IconChevronRight size={14} /></ActionIcon></Tooltip>
        <Text size="xs" ff="monospace" data-timecode>{timecode(now)}{length > 0 ? ' / ' + timecode(length) : ''}</Text>
        {mark?.command && !marking && (
          <Button size="compact-xs" variant="light" leftSection={<IconFlag size={12} />} ml="auto" onClick={() => { media.current?.pause(); setMarking(true); }} data-mark>
            {mark.label || t('verb.' + (mark.verb ?? 'mark'), { defaultValue: mark.verb })} {timecode(Math.round(now * 100) / 100)}
          </Button>
        )}
      </Group>
      {marking && mark && (mark.input ? <ActionInput act={mark} busy={busy} onSend={send} onCancel={() => setMarking(false)} />
        : <Group gap="xs"><Button size="xs" loading={busy} onClick={() => send('')}>{t('verb.send')}</Button><Button size="xs" variant="subtle" onClick={() => setMarking(false)}>{t('verb.cancel')}</Button></Group>)}
      </Stack>
      {!!it.marks?.length && (
        <div className={s.marks} data-marks>
          {it.marks.map((m, i) => (
            <Badge key={i} variant="light" radius="sm" className={s.mark} onClick={() => go(m.at)} data-mark-at={m.at}>
              {timecode(m.at)}{m.text ? ' · ' + m.text : ''}
            </Badge>
          ))}
        </div>
      )}
      </div>
    </Stack>
  );
}
