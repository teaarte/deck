import { useState } from 'react';
import { Alert, Group, Image, Loader, Modal, Stack, UnstyledButton } from '@mantine/core';
import { Download } from './Download';
import { streamed, type Item } from './kinds';
import { useBlob } from './useBlob';

// Picture is a thumbnail that opens the picture in full; a wide one takes the width of its column. It shows the light
// copy in its own proportions while the picture itself is read only to be opened.
export function Picture({ it, wide }: { it: Item; wide?: boolean }) {
  const [open, setOpen] = useState(false);
  const light = it.thumb ? streamed(it.thumb) : undefined;
  const { src, error } = useBlob(light && !open ? undefined : it.url, it.type);
  if (error) return <Alert color="red" p="xs">{error}</Alert>;
  const shown = light ?? src;
  if (!shown) return <Loader size="xs" />;
  const ratio = it.width && it.height ? `${it.width} / ${it.height}` : undefined;
  return (
    <>
      <UnstyledButton onClick={() => setOpen(true)} data-media="image">
        <Image src={shown} alt={it.title || it.name} h={wide ? 'auto' : 160} mah={wide ? '70vh' : undefined} fit={wide ? 'contain' : 'cover'}
          style={{ aspectRatio: wide ? ratio : undefined, background: it.color, borderRadius: 'var(--mantine-radius-sm)' }} />
      </UnstyledButton>
      <Modal opened={open} onClose={() => setOpen(false)} size="auto" title={it.title || it.name} centered>
        <Stack gap="xs">
          {src ? <Image src={src} alt={it.title || it.name} maw="90vw" mah="80vh" fit="contain" data-media-full /> : <Loader size="xs" />}
          <Group justify="flex-end"><Download it={it} /></Group>
        </Stack>
      </Modal>
    </>
  );
}
