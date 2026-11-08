import type { ReactNode } from 'react';
import { Group, Text } from '@mantine/core';
import { Logo } from './logo/Logo';
import { NamedIcon } from './NamedIcon';

// Facts are what an option of a picker shows beside its label: the mark of its model or provider, or its icon, and a
// short note such as price and speed.
export type Facts = { logo?: string; icon?: string; note?: string };

// Mark is the mark of an option: of its model or provider by logo, else its icon; nothing without either.
export function Mark({ facts, size = 16 }: { facts?: Facts; size?: number }) {
  if (facts?.logo) return <Logo names={facts.logo} size={size} />;
  if (facts?.icon) return <NamedIcon name={facts.icon} fallback="" size={size} />;
  return null;
}

// OptionLine is an option of a picker on one line: its mark, its label and its note, dimmed, at the end.
export function OptionLine({ label, facts, lead }: { label: string; facts?: Facts; lead?: ReactNode }) {
  return (
    <Group gap={8} wrap="nowrap" w="100%" data-option={label}>
      {lead}
      <Mark facts={facts} />
      <Text size="sm" className="deck-grow" truncate>{label}</Text>
      {facts?.note && <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }} data-option-note>{facts.note}</Text>}
    </Group>
  );
}
