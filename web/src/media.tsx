import { useEffect, useState } from 'react';
import { Alert, Button, Group, Image, Loader, Modal, SimpleGrid, Stack, Text, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { fetchFile, saveBlob } from './api';
import { formatted } from './format';
import type { Data } from './catalog.gen';

type Item = NonNullable<Data['files']>[number];

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
      } finally {
        setBusy(false);
      }
    }}>{t('verb.download')}</Button>
  );
}

function Picture({ it }: { it: Item }) {
  const [open, setOpen] = useState(false);
  const { src, error } = useBlob(it.url);
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

// Pdf draws the file as a PDF whatever deck says its type is, so it never opens as a page of deck.
function Pdf({ it }: { it: Item }) {
  const { src, error } = useBlob(it.url, 'application/pdf');
  if (error) return <Alert color="red" p="xs">{error}</Alert>;
  if (!src) return <Loader size="xs" />;
  return <object data={src} type="application/pdf" width="100%" height={480} data-media="pdf" aria-label={it.title || it.name} />;
}

// Media shows the files of a source: a picture as a thumbnail that opens in full, a PDF in place, the rest by
// name and size with a download.
export function Media({ items }: { items: Item[] }) {
  const pdfs = items.filter((it) => it.type === 'application/pdf');
  const rest = items.filter((it) => it.type !== 'application/pdf');
  return (
    <Stack gap="sm">
      <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="sm">
        {rest.filter((it) => !it.error && it.type?.startsWith('image/')).map((it) => (
          <Stack key={String(it.id)} gap={4} data-item><Picture it={it} /><Text size="xs" truncate>{it.title || it.name}</Text></Stack>
        ))}
      </SimpleGrid>
      {pdfs.filter((it) => !it.error).map((it) => (
        <Stack key={String(it.id)} gap={4} data-item>
          <Group justify="space-between"><Text size="sm" fw={500}>{it.title || it.name}</Text><Download it={it} /></Group>
          <Pdf it={it} />
        </Stack>
      ))}
      {items.filter((it) => it.error || !(it.type?.startsWith('image/') || it.type === 'application/pdf')).map((it) => (
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
