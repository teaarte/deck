import { Group, Stack, Table, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { text } from '../../lib/format';
import { kind, type Schema } from './schema';

// Result draws what a command returned by its result schema: a number, a text, a list or a table.
export function Result({ value, schema }: { value: unknown; schema?: Schema }) {
  const { t } = useTranslation();
  if (value === null || value === undefined) return <Text size="sm" c="dimmed">{t('form.nothing')}</Text>;
  const k = schema ? kind(schema) : Array.isArray(value) ? 'array' : typeof value;
  if (k === 'array' && Array.isArray(value)) {
    const objects = value.every((v) => v && typeof v === 'object' && !Array.isArray(v));
    if (!objects) return <Stack gap={2}>{value.map((v, i) => <Text key={i} size="sm">{text(v)}</Text>)}</Stack>;
    const cols = schema?.items?.properties ? Object.keys(schema.items.properties)
      : [...new Set(value.flatMap((v) => Object.keys(v as object)))];
    return (
      <Table.ScrollContainer minWidth={300}>
        <Table striped data-result="table">
          <Table.Thead><Table.Tr>{cols.map((c) => <Table.Th key={c}>{schema?.items?.properties?.[c]?.title || c}</Table.Th>)}</Table.Tr></Table.Thead>
          <Table.Tbody>
            {value.map((row, i) => (
              <Table.Tr key={i}>{cols.map((c) => <Table.Td key={c}>{text((row as Record<string, unknown>)[c])}</Table.Td>)}</Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    );
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const o = value as Record<string, unknown>;
    return (
      <Stack gap={4} data-result="object">
        {Object.keys(o).map((key) => (
          <Group key={key} gap="sm" wrap="nowrap" align="flex-start">
            <Text size="sm" c="dimmed" w={140}>{schema?.properties?.[key]?.title || key}</Text>
            {o[key] !== null && typeof o[key] === 'object'
              ? <Result value={o[key]} schema={schema?.properties?.[key]} />
              : <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{text(o[key])}</Text>}
          </Group>
        ))}
      </Stack>
    );
  }
  if (typeof value === 'number') return <Text fz={32} fw={700} data-result="number">{value}</Text>;
  return <Text size="sm" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }} data-result="text">{text(value)}</Text>;
}
