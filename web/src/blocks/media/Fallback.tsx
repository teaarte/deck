import { Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { formatted } from '../../lib/format';
import { Download } from './Download';
import type { Item } from './kinds';
import { TypeIcon } from './TypeIcon';

// Fallback is a file deck cannot open: its badge, name and size, and its download.
export function Fallback({ it }: { it: Item }) {
  const { t } = useTranslation();
  return (
    <Stack align="center" gap="xs" py="lg" data-media="fallback">
      <TypeIcon it={it} size={72} />
      <Text size="sm" fw={500} ta="center" style={{ overflowWrap: 'anywhere' }}>{it.name}</Text>
      <Text size="xs" c="dimmed">{[formatted(it.size, { format: 'bytes' }), t('files.no_preview')].join(' · ')}</Text>
      <Download it={it} />
    </Stack>
  );
}
