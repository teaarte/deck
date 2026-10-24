import { Badge, Group, Text, Tooltip } from '@mantine/core';
import type { ReactNode } from 'react';
import { IconActivity, IconAlertCircleFilled, IconHandFinger } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';

// NavStatus shows what a project is busy with: failing health, questions to the owner, running processes; each an icon
// with its word, said beside it with words or in a hint without.
export function NavStatus({ m, words }: { m: MenuEntry; words?: boolean }) {
  const { t } = useTranslation();
  const errors = t('nav.errors', { n: m.errors });
  const asks = t('nav.asks', { n: m.asks });
  const running = t('nav.running', { n: m.running });
  const said = (icon: ReactNode, label: string) => words
    ? <Group gap={5} wrap="nowrap">{icon}<Text size="sm" fw={600} c="inherit">{label}</Text></Group>
    : <Tooltip label={label}>{icon}</Tooltip>;
  if (!m.errors && !m.asks && !m.running) return null;
  return (
    <Group gap={words ? 12 : 6} wrap={words ? 'wrap' : 'nowrap'} data-status>
      {!!m.errors && said(<IconAlertCircleFilled size={16} color="var(--mantine-color-red-6)" role="img" aria-label={errors} data-errors={m.errors} style={{ flex: 'none' }} />, errors)}
      {!!m.asks && said(words ? <IconHandFinger size={16} color="var(--mantine-color-orange-7)" role="img" aria-label={asks} data-asks={m.asks} style={{ flex: 'none' }} /> : <Badge size="sm" circle color="orange" variant="light" aria-label={asks} data-asks={m.asks}>{m.asks}</Badge>, asks)}
      {!!m.running && said(<IconActivity size={15} color="var(--mantine-color-green-7)" role="img" aria-label={running} data-running={m.running} style={{ flex: 'none' }} />, running)}
    </Group>
  );
}
