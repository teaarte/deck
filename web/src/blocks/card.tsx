import { Group, Spoiler, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { CardValue, Data } from '../lib/catalog.gen';
import { Actions } from '../ui/actions/Actions';
import { Markdown } from '../ui/Markdown';
import { Value } from '../ui/Value';
import { blockImpl } from './blockImpl';

type Field = CardValue['fields'][number];

// short are fields that fit a line of their own kind: tags and times.
const short = (f: Field) => !f.markdown && ['tag', 'relative', 'date', 'datetime', 'logo'].includes(f.format ?? '');

// LONG is how many characters a value shows before it folds under Show more.
const LONG = 400;
const long = (v: unknown) => typeof v === 'string' && v.length > LONG;

// card draws the fields of one record: the short ones in one line, the rest label by value, a long one folded, and
// its buttons.
export const card = blockImpl('card', (_, d) => <Card d={d} />);

function Card({ d }: { d: Data }) {
  const { t } = useTranslation();
  const v = (d.value ?? {}) as Partial<CardValue>;
  const fields = v.fields ?? [];
  const inline = fields.filter(short);
  return (
    <Stack gap="xs">
      {inline.length > 0 && (
        <Group gap="md" data-card-short>
          {inline.map((f) => (
            <Group key={f.label} gap={6} wrap="nowrap">
              <Text size="xs" c="dimmed">{f.label}</Text>
              <Text size="sm" component="div"><Value v={f.value} s={f} tone={f.tone} /></Text>
            </Group>
          ))}
        </Group>
      )}
      {fields.filter((f) => !short(f)).map((f) => (
        f.markdown && typeof f.value === 'string' ? (
          <Stack key={f.label} gap={2}>
            <Text size="sm" c="dimmed">{f.label}</Text>
            <Markdown text={f.value} />
          </Stack>
        ) : (
          <Group key={f.label} gap="sm" wrap="nowrap" align="flex-start">
            <Text size="sm" c="dimmed" w={120}>{f.label}</Text>
            {long(f.value)
              ? <Spoiler maxHeight={96} showLabel={t('Show more')} hideLabel={t('Show less')} miw={0} style={{ flex: 1 }} data-card-long>
                <Text size="sm" component="div" className="deck-pre"><Value v={f.value} s={f} tone={f.tone} /></Text>
              </Spoiler>
              : <Text size="sm" component="div"><Value v={f.value} s={f} tone={f.tone} /></Text>}
          </Group>
        )
      ))}
      <Actions actions={v.actions} />
    </Stack>
  );
}
