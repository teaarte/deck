import { useEffect, useState } from 'react';
import { Alert, Code, Image, Loader, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { Fallback } from './Fallback';
import { fetchText, kindOf, PREVIEW, streamed, type Item } from './kinds';
import { Pdf } from './Pdf';
import { useBlob } from './useBlob';
import s from './files.module.css';

// Picture is a picture in full, from a copy the page made of it.
function Full({ it }: { it: Item }) {
  const { src, error } = useBlob(it.url, it.type);
  if (error) return <Alert color="red" p="xs">{error}</Alert>;
  if (!src) return <Loader size="xs" />;
  return <Image src={src} alt={it.title || it.name} maw="100%" mah="75vh" fit="contain" data-media-full />;
}

// Native is a PDF in the viewer of the browser, from a copy the page made of it.
function Native({ it }: { it: Item }) {
  const { src, error } = useBlob(it.url, 'application/pdf');
  if (error) return <Alert color="red" p="xs">{error}</Alert>;
  if (!src) return <Loader size="xs" />;
  return <iframe src={src} title={it.title || it.name} style={{ width: '100%', height: '75vh', border: 0 }} data-media="pdf-native" />;
}

// Texty is the start of a text, or an HTML or SVG page in a frame that may run nothing and reach nothing.
function Texty({ it, framed }: { it: Item; framed: boolean }) {
  const { t } = useTranslation();
  const [got, setGot] = useState<{ text: string; cut: boolean; error?: string }>();
  useEffect(() => {
    let gone = false;
    fetchText(it.url!).then((v) => { if (!gone) setGot(v); }, (e: Error) => { if (!gone) setGot({ text: '', cut: false, error: e.message }); });
    return () => { gone = true; };
  }, [it.url]);
  if (got?.error) return <Alert color="red" p="xs">{got.error}</Alert>;
  if (!got) return <Loader size="xs" />;
  return (
    <Stack gap={4}>
      {framed
        ? <iframe sandbox="" srcDoc={got.text} title={it.title || it.name} style={{ width: '100%', height: '70vh', border: 0, background: 'var(--mantine-color-white)' }} data-media="frame" />
        : <Code block style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxHeight: '70vh', overflow: 'auto' }} data-media="text">{got.text}</Code>}
      {got.cut && <Text size="xs" c="dimmed">{t('files.cut')}</Text>}
    </Stack>
  );
}

// Viewer opens a file the way it can: the browser itself first, then deck's own, else its card with a download.
export function Viewer({ it }: { it: Item }) {
  if (it.error) return <Alert color="red" p="xs">{it.error}</Alert>;
  const big = (it.size ?? 0) > PREVIEW;
  switch (kindOf(it)) {
    case 'image':
      return big ? <Fallback it={it} /> : <Full it={it} />;
    case 'video':
      return <video src={streamed(it.url)} controls playsInline preload="metadata" className={s.media} data-player="video" />;
    case 'audio':
      return <audio src={streamed(it.url)} controls preload="metadata" className={s.media} data-player="audio" />;
    case 'pdf':
      if (big) return <Fallback it={it} />;
      return navigator.pdfViewerEnabled ? <Native it={it} /> : <Pdf it={it} />;
    case 'html':
    case 'svg':
      return <Texty it={it} framed />;
    case 'text':
      return <Texty it={it} framed={false} />;
  }
  return <Fallback it={it} />;
}
