import { useState } from 'react';
import { Alert, Group, Image, Loader, Modal, Stack, UnstyledButton } from '@mantine/core';
import { Download } from './Download';
import type { Item } from './Media';
import { useBlob } from './useBlob';

// Picture is a thumbnail that opens the picture in full.
export function Picture({ it }: { it: Item }) {
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
