import { useMemo, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Group, Pagination, Select, Table, Text, TextInput, UnstyledButton } from '@mantine/core';
import { IconArrowDown, IconArrowUp, IconSearch, IconSelector } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Actions } from './ui/Actions';
import { rowOpen } from './ui/rowOpen';
import { show, text, type Shown } from './lib/format';
import { Value } from './ui/Value';
import type { Data } from './lib/catalog.gen';
import s from './shell.module.css';

type Row = NonNullable<Data['rows']>[number];
type Column = Shown & { label: string };

const SIZES = ['10', '25', '50', '100'];

function compare(a: unknown, b: unknown): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return text(a).localeCompare(text(b), undefined, { numeric: true, sensitivity: 'base' });
}

// Cell holds a value in at most three lines; a click on a cut one shows it whole.
function Cell({ children }: { children: React.ReactNode }) {
  const [whole, setWhole] = useState(false);
  return (
    <div className={s.clamp} data-whole={whole || undefined} onClick={(e) => {
      const el = e.currentTarget;
      if (!whole && el.scrollHeight > el.clientHeight + 1) { e.stopPropagation(); setWhole(true); }
    }}>{children}</div>
  );
}

// DataTable is a table block: search over its rows, sort by a column, pages, and the buttons of each row.
export function DataTable({ columns, rows, context }: { columns: Column[]; rows: Row[]; context: ComponentContext }) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<{ col: number; desc: boolean }>();
  const [size, setSize] = useState('25');
  const [page, setPage] = useState(1);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    let out = q ? rows.filter((r) => r.cells.some((c) => text(c).toLowerCase().includes(q))) : rows;
    if (sort) out = [...out].sort((a, b) => compare(a.cells[sort.col], b.cells[sort.col]) * (sort.desc ? -1 : 1));
    return out;
  }, [rows, query, sort]);
  const per = Number(size);
  const pages = Math.max(1, Math.ceil(shown.length / per));
  const at = Math.min(page, pages);
  const slice = shown.slice((at - 1) * per, at * per);
  const acts = rows.some((r) => r.actions?.length);
  const flip = (col: number) => setSort((x) => (x?.col !== col ? { col, desc: false } : x.desc ? undefined : { col, desc: true }));
  return (
    <div data-table>
      {rows.length > 10 && (
        <TextInput size="xs" radius="xl" mb="sm" maw={280} leftSection={<IconSearch size={14} />} placeholder={t('table.search')}
          value={query} onChange={(e) => { setQuery(e.currentTarget.value); setPage(1); }} data-table-search />
      )}
      <Table.ScrollContainer minWidth={Math.min(900, 160 * columns.length + (acts ? 160 : 0))}>
        <Table highlightOnHover verticalSpacing="sm" className={s.table}>
          <Table.Thead>
            <Table.Tr>
              {columns.map((c, i) => (
                <Table.Th key={i} aria-sort={sort?.col === i ? (sort.desc ? 'descending' : 'ascending') : undefined}>
                  <UnstyledButton onClick={() => flip(i)} className={s.sort} data-sort={i}>
                    {c.label}
                    {sort?.col === i ? (sort.desc ? <IconArrowDown size={12} /> : <IconArrowUp size={12} />) : <IconSelector size={12} className={s.sortHint} />}
                  </UnstyledButton>
                </Table.Th>
              ))}
              {acts && <Table.Th />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {slice.map((r, i) => (
              <Table.Tr key={show(r.id) || i} {...rowOpen(r.actions, context)}>
                {r.cells.map((v, j) => <Table.Td key={j}><Cell><Value v={v} s={columns[j] ?? {}} tone={r.tones?.[j]} /></Cell></Table.Td>)}
                {acts && <Table.Td className={s.rowActions}><Actions actions={r.actions} context={context} max={2} /></Table.Td>}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!slice.length && query && <Text size="sm" c="dimmed" mt="sm">{t('table.nothing')}</Text>}
      {shown.length > per && (
        <Group justify="space-between" mt="sm" data-table-pages>
          <Text size="xs" c="dimmed">{t('table.range', { from: (at - 1) * per + 1, to: Math.min(at * per, shown.length), of: shown.length })}</Text>
          <Group gap="xs">
            <Select size="xs" w={76} data={SIZES} value={size} onChange={(v) => { setSize(v ?? '25'); setPage(1); }} aria-label={t('table.size')} allowDeselect={false} />
            <Pagination size="sm" radius="xl" total={pages} value={at} onChange={setPage} />
          </Group>
        </Group>
      )}
    </div>
  );
}
