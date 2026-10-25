import { useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Group, Select, TextInput } from '@mantine/core';
import { useDebouncedCallback } from '@mantine/hooks';
import { IconSearch } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Data } from '../lib/catalog.gen';
import { readBlock } from '../screen/press';

type Filter = NonNullable<Data['filters']>[number];

// Filters narrow a block by the values its source has and by the words the owner types; a pick reads the block
// again with it.
export function Filters({ filters, context }: { filters?: Filter[]; context?: ComponentContext }) {
  const { t } = useTranslation();
  if (!filters?.length || !context) return null;
  const pick = (field: string, value: string | null) => readBlock(context, 'filter', { field, value: value ?? '' });
  return (
    <Group gap="xs">
      {filters.map((f) => f.search ? <Search key={f.field} value={f.value ?? ''} onSearch={(v) => pick(f.field, v)} placeholder={t('filter.search')} /> : (
        <Select key={f.field} size="xs" w={180} clearable placeholder={t('filter.any', { field: f.label ?? f.field })} data={f.options.map((o, i) => ({ value: o, label: f.labels?.[i] ?? o }))}
          value={f.value || null} onChange={(v) => pick(f.field, v)} data-filter={f.field} />
      ))}
    </Group>
  );
}

// Search sends the words typed once the owner stops typing.
function Search({ value, onSearch, placeholder }: { value: string; onSearch: (v: string) => void; placeholder: string }) {
  const [text, setText] = useState(value);
  const send = useDebouncedCallback(onSearch, 300);
  return (
    <TextInput size="xs" w={220} value={text} placeholder={placeholder} leftSection={<IconSearch size={14} />} data-search
      onChange={(e) => { setText(e.currentTarget.value); send(e.currentTarget.value.trim()); }} />
  );
}
