import { useEffect, useRef, useState } from 'react';
import { notifications } from '@mantine/notifications';
import { Alert, Button, Group, Image, Loader, Modal, SimpleGrid, Stack, Text, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { fetchFile, saveBlob } from './lib/api';
import { formatted } from './lib/format';
import type { Data } from './lib/catalog.gen';

type Item = NonNullable<Data['files']>[number];

// PICTURES are the types drawn as a picture: raster only, so no file runs as a script of deck.
const PICTURES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif', 'image/bmp'];
// PREVIEW is the largest file read for a preview; a larger one is only a download.
const PREVIEW = 20 * 1000 * 1000;
const shows = (it: Item, types: string[]) => !it.error && types.includes(it.type ?? '') && (it.size ?? 0) <= PREVIEW;
const isPicture = (it: Item) => shows(it, PICTURES);
const isPdf = (it: Item) => shows(it, ['application/pdf']);

// useBlob reads a file of the item through the API with the token and gives an address of it for the page.
function useBlob(url: string | undefined, type?: string) {
  const [state, setState] = useState<{ src?: string; blob?: Blob; error?: string }>({});
  useEffect(() => {
    if (!url) return;
    let gone = false;
    let src = '';
    fetchFile(url).then((blob) => {
      src = URL.createObjectURL(type ? new Blob([blob], { type }) : blob);
      if (gone) URL.revokeObjectURL(src);
      else setState({ src, blob });
    }, (e: Error) => { if (!gone) setState({ error: e.message }); });
    return () => { gone = true; if (src) URL.revokeObjectURL(src); };
  }, [url, type]);
  return state;
}

function Download({ it }: { it: Item }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  return (
    <Button size="xs" variant="default" loading={busy} data-download onClick={async () => {
      setBusy(true);
      try {
        saveBlob(await fetchFile(it.url!), it.name ?? 'file');
      } catch (e) {
        notifications.show({ title: it.title || it.name, message: (e as Error).message, color: 'red' });
      } finally {
        setBusy(false);
      }
    }}>{t('verb.download')}</Button>
  );
}

function Picture({ it }: { it: Item }) {
  const [open, setOpen] = useState(false);
  const { src, error } = useBlob(it.url, it.type);
  if (error) return <Alert color="red" p="xs">{error}</Alert>;
  if (!src) return <Loader size="xs" />;
  return (
    <>
      <UnstyledButton onClick={() => setOpen(true)} data-media="image">
        <Image src={src} alt={it.title || it.name} h={160} fit="cover" radius="sm" />
      </UnstyledButton>
      <Modal opened={open} onClose={() => setOpen(false)} size="auto" title={it.title || it.name} centered>
        <Stack gap="xs">
          <Image src={src} alt={it.title || it.name} maw="90vw" mah="80vh" fit="contain" data-media-full />
          <Group justify="flex-end"><Download it={it} /></Group>
        </Stack>
      </Modal>
    </>
  );
}

// PAGES is how many pages of a PDF are drawn in place; the rest is in the download.
const PAGES = 10;

// Pdf draws the pages of a PDF on canvases with pdf.js: no frame, so the page of deck frames nothing.
function Pdf({ it }: { it: Item }) {
  const { blob, error } = useBlob(it.url, 'application/pdf');
  const box = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState('');
  useEffect(() => {
    const el = box.current;
    if (!blob || !el) return;
    let gone = false;
    let task: { destroy: () => Promise<void> } | undefined;
    void (async () => {
      try {
        const pdfjs = await import('pdfjs-dist');
        pdfjs.GlobalWorkerOptions.workerSrc = (await import('pdfjs-dist/build/pdf.worker.min.mjs?url')).default;
        const loading = pdfjs.getDocument({ data: new Uint8Array(await blob.arrayBuffer()) });
        task = loading;
        const doc = await loading.promise;
        for (let n = 1; n <= Math.min(doc.numPages, PAGES) && !gone; n++) {
          const page = await doc.getPage(n);
          const width = el.clientWidth || 600;
          const viewport = page.getViewport({ scale: (width / page.getViewport({ scale: 1 }).width) * devicePixelRatio });
          const canvas = document.createElement('canvas');
          canvas.width = viewport.width;
          canvas.height = viewport.height;
          canvas.style.width = '100%';
          canvas.style.border = '1px solid var(--mantine-color-default-border)';
          canvas.dataset.page = String(n);
          el.appendChild(canvas);
          await page.render({ canvas, viewport }).promise;
        }
      } catch (e) {
        if (!gone) setFailed((e as Error).message);
      }
    })();
    return () => { gone = true; void task?.destroy(); el.replaceChildren(); };
  }, [blob]);
  if (error || failed) return <Alert color="red" p="xs">{error || failed}</Alert>;
  if (!blob) return <Loader size="xs" />;
  return <Stack ref={box} gap="xs" data-media="pdf" aria-label={it.title || it.name} />;
}

// Media shows the files of a source: a picture as a thumbnail that opens in full, a PDF in place, the rest by
// name and size with a download.
export function Media({ items }: { items: Item[] }) {
  return (
    <Stack gap="sm">
      <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="sm">
        {items.filter(isPicture).map((it) => (
          <Stack key={String(it.id)} gap={4} data-item><Picture it={it} /><Text size="xs" truncate>{it.title || it.name}</Text></Stack>
        ))}
      </SimpleGrid>
      {items.filter(isPdf).map((it) => (
        <Stack key={String(it.id)} gap={4} data-item>
          <Group justify="space-between"><Text size="sm" fw={500}>{it.title || it.name}</Text><Download it={it} /></Group>
          <Pdf it={it} />
        </Stack>
      ))}
      {items.filter((it) => !isPicture(it) && !isPdf(it)).map((it) => (
        <Group key={String(it.id)} justify="space-between" wrap="nowrap" data-item data-media={it.error ? 'error' : 'file'}>
          <Stack gap={0} miw={0}>
            <Text size="sm" fw={500} truncate>{it.title || it.name}</Text>
            {it.error ? <Text size="xs" c="red">{it.error}</Text>
              : <Text size="xs" c="dimmed">{[it.name !== it.title && it.title ? it.name : '', formatted(it.size, { format: 'bytes' })].filter(Boolean).join(' · ')}</Text>}
          </Stack>
          {!it.error && <Download it={it} />}
        </Group>
      ))}
    </Stack>
  );
}
