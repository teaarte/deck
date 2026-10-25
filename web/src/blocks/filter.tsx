import { useContext, useEffect } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Select } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { setParam } from '../app/router';
import type { FilterProps } from '../lib/catalog.gen';
import { Pending } from '../ui/Pending';
import { api } from './blockImpl';
import { Picks } from './side';

// filter is a pick of the side panel that sets a param of the screen in its address; the screen is read again with it.
export const filter = createComponentImplementation(api('filter'), ({ props }) => {
  const p = props as FilterProps & { data?: { filters?: { field: string; label?: string; value?: string; options: string[]; labels?: string[] }[] } };
  const { t } = useTranslation();
  const mark = useContext(Picks);
  const f = p.data?.filters?.[0];
  const value = f?.value || null;
  useEffect(() => mark(p.param, !!value), [mark, p.param, value]);
  if (!f) return <Pending />;
  return (
    <Select size="sm" label={f.label || p.title} clearable placeholder={t('filter.all')} value={value} comboboxProps={{ withinPortal: true }}
      data={f.options.map((o, i) => ({ value: o, label: f.labels?.[i] ?? o }))} onChange={(v) => setParam(f.field, v)} data-side-filter={f.field} />
  );
});
