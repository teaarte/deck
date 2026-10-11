import { Group, Pagination, Select, Table, Text, TextInput, UnstyledButton } from '@mantine/core';
import { IconArrowDown, IconArrowUp, IconSearch, IconSelector } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './table.module.css';
import { show, type Shown } from '../../lib/format';
import { Actions } from '../../ui/actions/Actions';
import { rowOpen } from '../../ui/rowOpen';
import { Value } from '../../ui/Value';
import { useScreenActions } from '../../screen/ScreenActions';
import { Cell } from './Cell';
import { Pick } from './Pick';
import { useTableView, type Row } from './useTableView';

type Column = Shown & { label: string; pick?: string };

const SIZES = ['10', '25', '50', '100'];

// DataTable is a table block: search over its rows, sort by a column, pages, and the buttons of each row.
export function DataTable({ columns, rows }: { columns: Column[]; rows: Row[] }) {
  const { t } = useTranslation();
  const v = useTableView(rows);
  const { run } = useScreenActions();
  const own = (r: Row) => r.actions?.filter((a) => !a.pick || !columns.some((c) => c.pick === a.pick));
  const acts = rows.some((r) => own(r)?.length);
  return (
    <div data-table>
      {rows.length > 10 && (
        <TextInput size="xs" radius="xl" mb="sm" maw={280} leftSection={<IconSearch size={14} />} placeholder={t('table.search')}
          value={v.query} onChange={(e) => v.search(e.currentTarget.value)} data-table-search />
      )}
      <Table.ScrollContainer minWidth={Math.min(900, 160 * columns.length + (acts ? 160 : 0))}>
        <Table highlightOnHover verticalSpacing="sm" className={s.table}>
          <Table.Thead>
            <Table.Tr>
              {columns.map((c, i) => (
                <Table.Th key={i} aria-sort={v.sort?.col === i ? (v.sort.desc ? 'descending' : 'ascending') : undefined}>
                  <UnstyledButton onClick={() => v.flip(i)} className={s.sort} data-sort={i}>
                    {c.label}
                    {v.sort?.col === i ? (v.sort.desc ? <IconArrowDown size={12} /> : <IconArrowUp size={12} />) : <IconSelector size={12} className={s.sortHint} />}
                  </UnstyledButton>
                </Table.Th>
              ))}
              {acts && <Table.Th />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {v.slice.map((r, i) => (
              <Table.Tr key={show(r.id) || i} {...rowOpen(r.actions, run)}>
                {r.cells.map((v, j) => {
                  const pick = columns[j]?.pick && r.actions?.find((a) => a.pick === columns[j].pick);
                  return <Table.Td key={j}>{pick ? <Pick act={pick} value={v} /> : <Cell details={r.details?.[j]}><Value v={v} s={columns[j] ?? {}} tone={r.tones?.[j]} /></Cell>}</Table.Td>;
                })}
                {acts && <Table.Td className={s.rowActions}><Actions actions={own(r)} max={2} /></Table.Td>}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {!v.slice.length && v.query && <Text size="sm" c="dimmed" mt="sm">{t('table.nothing')}</Text>}
      {v.shown.length > v.per && (
        <Group justify="space-between" mt="sm" data-table-pages>
          <Text size="xs" c="dimmed">{t('table.range', { from: (v.at - 1) * v.per + 1, to: Math.min(v.at * v.per, v.shown.length), of: v.shown.length })}</Text>
          <Group gap="xs">
            <Select size="xs" w={76} data={SIZES} value={v.size} onChange={(x) => v.resize(x ?? '25')} aria-label={t('table.size')} allowDeselect={false} />
            <Pagination size="sm" radius="xl" total={v.pages} value={v.at} onChange={v.setPage} />
          </Group>
        </Group>
      )}
    </div>
  );
}
