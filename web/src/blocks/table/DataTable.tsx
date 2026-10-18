import { Fragment, useState } from 'react';
import { ActionIcon, Group, Pagination, Select, Table, Text, TextInput, UnstyledButton } from '@mantine/core';
import { IconArrowDown, IconArrowUp, IconChevronRight, IconSearch, IconSelector } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './table.module.css';
import { show, text, type Shown } from '../../lib/format';
import { usePhone } from '../../hooks/usePhone';
import { Actions } from '../../ui/actions/Actions';
import { rowOpen } from '../../ui/rowOpen';
import { Value } from '../../ui/Value';
import { useScreenActions } from '../../screen/ScreenActions';
import { Cell } from './Cell';
import { Pick } from './Pick';
import { useTableView, type Row } from './useTableView';

type Column = Shown & { label: string; pick?: string };

const SIZES = ['10', '25', '50', '100'];
// MAIN is how many columns a row shows in its line, first by their order; the rest open under it.
const MAIN = { desktop: 4, phone: 2 };

const empty = (v: unknown) => text(v).trim() === '' || (Array.isArray(v) && v.length === 0);

// DataTable is a table block: the first columns in each line, the rest and the long texts in the row opened under
// it, no column every row leaves empty, search over its rows, sort by a column, pages, and the buttons of each row.
export function DataTable({ columns, rows }: { columns: Column[]; rows: Row[] }) {
  const { t } = useTranslation();
  const phone = usePhone();
  const v = useTableView(rows);
  const { run } = useScreenActions();
  const [opened, setOpened] = useState<Record<string, boolean>>({});
  const pickOf = (r: Row, j: number) => columns[j]?.pick && r.actions?.find((a) => a.pick === columns[j].pick);
  const own = (r: Row) => r.actions?.filter((a) => !a.pick || !columns.some((c) => c.pick === a.pick));
  const acts = rows.some((r) => own(r)?.length);
  const shown = columns.map((_, j) => j).filter((j) => rows.some((r) => !empty(r.cells[j]) || pickOf(r, j) || !empty(r.details?.[j])));
  const main = shown.slice(0, phone ? MAIN.phone : MAIN.desktop);
  const rest = shown.slice(main.length);
  const more = (r: Row) => rest.some((j) => !empty(r.cells[j]) || pickOf(r, j)) || shown.some((j) => !empty(r.details?.[j]));
  const opens = rows.some(more);
  const cell = (r: Row, j: number) => {
    const pick = pickOf(r, j);
    if (pick) return <Pick act={pick} value={r.cells[j]} />;
    return <Value v={r.cells[j]} s={columns[j] ?? {}} tone={r.tones?.[j]} hint={columns[j]?.format === 'tag' ? r.details?.[j] : undefined} />;
  };
  return (
    <div data-table>
      {rows.length > 10 && (
        <TextInput size="xs" radius="xl" mb="sm" maw={280} leftSection={<IconSearch size={14} />} placeholder={t('table.search')}
          value={v.query} onChange={(e) => v.search(e.currentTarget.value)} data-table-search />
      )}
      <Table.ScrollContainer minWidth={Math.min(720, 150 * main.length + (acts ? 140 : 0))}>
        <Table highlightOnHover verticalSpacing="sm" className={s.table}>
          <Table.Thead>
            <Table.Tr>
              {opens && <Table.Th className={s.toggleCol} />}
              {main.map((j) => (
                <Table.Th key={j} aria-sort={v.sort?.col === j ? (v.sort.desc ? 'descending' : 'ascending') : undefined}>
                  <UnstyledButton onClick={() => v.flip(j)} className={s.sort} data-sort={j}>
                    {columns[j].label}
                    {v.sort?.col === j ? (v.sort.desc ? <IconArrowDown size={12} /> : <IconArrowUp size={12} />) : <IconSelector size={12} className={s.sortHint} />}
                  </UnstyledButton>
                </Table.Th>
              ))}
              {acts && <Table.Th />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {v.slice.map((r, i) => {
              const key = show(r.id) || String(i);
              const open = !!opened[key];
              const flip = () => setOpened((o) => ({ ...o, [key]: !o[key] }));
              const opener = rowOpen(r.actions, run);
              const click = 'onClick' in opener ? opener : more(r) ? { style: { cursor: 'pointer' }, onClick: (e: { target: EventTarget }) => {
                if ((e.target as HTMLElement).closest('button, a, input, textarea, [role="button"], [role="combobox"]') || window.getSelection()?.toString()) return;
                flip();
              } } : {};
              return (
                <Fragment key={key}>
                  <Table.Tr {...click} data-row-open={open || undefined}>
                    {opens && (
                      <Table.Td className={s.toggleCol}>
                        {more(r) && (
                          <ActionIcon variant="subtle" color="gray" size="sm" onClick={flip} aria-expanded={open} aria-label={t(open ? 'table.less' : 'table.more')} data-row-toggle>
                            <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />
                          </ActionIcon>
                        )}
                      </Table.Td>
                    )}
                    {main.map((j) => <Table.Td key={j}>{pickOf(r, j) ? cell(r, j) : <Cell>{cell(r, j)}</Cell>}</Table.Td>)}
                    {acts && <Table.Td className={s.rowActions}><Actions actions={own(r)} max={2} /></Table.Td>}
                  </Table.Tr>
                  {open && (
                    <Table.Tr className={s.more} data-row-more>
                      <Table.Td colSpan={main.length + (acts ? 1 : 0) + 1}>
                        <dl className={s.fields}>
                          {rest.filter((j) => !empty(r.cells[j]) || pickOf(r, j)).map((j) => (
                            <Fragment key={j}><dt>{columns[j].label}</dt><dd>{cell(r, j)}</dd></Fragment>
                          ))}
                          {shown.filter((j) => !empty(r.details?.[j])).map((j) => (
                            <Fragment key={'d' + j}><dt>{columns[j].label}</dt><dd className={s.long}>{r.details![j]}</dd></Fragment>
                          ))}
                        </dl>
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Fragment>
              );
            })}
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
