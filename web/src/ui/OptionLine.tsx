import type { ReactNode } from 'react';
import { Group, Text } from '@mantine/core';
import { Logo } from './logo/Logo';
import { NamedIcon } from './NamedIcon';

// Facts are what an option of a picker shows beside its label: the mark of its model or provider, or its icon, and a
// short note such as price and speed; via the provider it is reached through, whose mark stands small in the corner.
export type Facts = { logo?: string; icon?: string; note?: string; via?: string };

// Mark is the mark of an option: of its model or provider by logo, else its icon; nothing without either.
export function Mark({ facts, size = 16 }: { facts?: Facts; size?: number }) {
  if (facts?.logo) return <Logo names={facts.logo} via={facts.via} size={size} />;
  if (facts?.icon) return <NamedIcon name={facts.icon} fallback="" size={size} />;
  return null;
}

// OptionLine is an option of a picker on one line: its mark, its label and its note, dimmed, at the end; the heading
// of its group says its provider.
export function OptionLine({ label, facts, lead }: { label: string; facts?: Facts; lead?: ReactNode }) {
  return (
    <Group gap={8} wrap="nowrap" w="100%" data-option={label}>
      {lead}
      <Mark facts={facts && { ...facts, via: undefined }} />
      <Text size="sm" className="deck-grow" truncate>{label}</Text>
      {facts?.note && <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }} data-option-note>{facts.note}</Text>}
    </Group>
  );
}

// bare is the label of an option under the heading of its group: without the group's first word before it, such as
// openrouter- or groq/, and without -free after it, which its note says.
export function bare(label: string, group?: string) {
  const by = group?.trim().split(/\s/)[0]?.toLowerCase();
  const out = by && label.toLowerCase().startsWith(by) && /[-/]/.test(label.charAt(by.length)) ? label.slice(by.length + 1) : label;
  return out.replace(/[-:]free$/i, '') || label;
}

// grouped are options under the headings of their groups, each group where its first option stands; options of no
// group stand alone before them.
export function grouped<T extends { value: string; label: string }>(opts: T[], groupOf: (o: T) => string | undefined) {
  if (!opts.some((o) => groupOf(o))) return opts;
  const loose: T[] = [];
  const groups = new Map<string, T[]>();
  for (const o of opts) {
    const g = groupOf(o);
    if (!g) loose.push(o);
    else groups.set(g, [...(groups.get(g) ?? []), { ...o, label: bare(o.label, g) }]);
  }
  return [...loose, ...[...groups].map(([group, items]) => ({ group, items }))];
}
