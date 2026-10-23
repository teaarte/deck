import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Group, Select } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Data } from '../lib/catalog.gen';
import { readBlock } from '../screen/press';

type Filter = NonNullable<Data['filters']>[number];

// Filters narrow a block by the values its source has; a pick reads the block again with it.
export function Filters({ filters, context }: { filters?: Filter[]; context?: ComponentContext }) {
  const { t } = useTranslation();
  if (!filters?.length || !context) return null;
  const pick = (field: string, value: string | null) => readBlock(context, 'filter', { field, value: value ?? '' });
  return (
    <Group gap="xs">
      {filters.map((f) => (
        <Select key={f.field} size="xs" w={180} clearable placeholder={t('filter.any', { field: f.label ?? f.field })} data={f.options.map((o, i) => ({ value: o, label: f.labels?.[i] ?? o }))}
          value={f.value || null} onChange={(v) => pick(f.field, v)} data-filter={f.field} />
      ))}
    </Group>
  );
}
