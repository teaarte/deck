import { FileInput, MultiSelect, NumberInput, PasswordInput, Select, Switch, TagsInput, TextInput, Textarea } from '@mantine/core';
import { text } from '../../lib/format';
import { fromLocal, isFile, kind, toLocal, type Schema } from './schema';

export type Choice = { value: unknown; label: string };

// Field is the input of one property by its schema: offered values, files, an enum, or by its type.
export function Field({ name, schema, value, files, readOnly, required, choices, onValue, onFiles }: {
  name: string; schema: Schema; value: unknown; files: File[]; readOnly: boolean; required: boolean; choices?: Choice[];
  onValue: (v: unknown) => void; onFiles: (f: File[]) => void;
}) {
  const label = schema.title || name;
  const common = { label, description: schema.description, required, disabled: readOnly, 'data-field': name };
  const t = kind(schema);
  // A list of an enum takes the values offered by their labels; another list takes those and others typed, such as
  // project.*; a single value is one of those offered.
  if (choices && t === 'array' && schema.items?.enum) {
    const data = [...new Map(choices.map((c) => [String(c.value), { value: String(c.value), label: c.label }])).values()];
    return <MultiSelect {...common} data={data}
      value={Array.isArray(value) ? value.map(String) : []}
      onChange={(vs) => onValue(vs.map((v) => choices.find((c) => String(c.value) === v)?.value))} />;
  }
  if (choices && t === 'array') {
    return <TagsInput {...common} data={[...new Set(choices.map((c) => String(c.value)))]} value={Array.isArray(value) ? value.map(String) : []}
      onChange={(vs) => onValue(['integer', 'number'].includes(kind(schema.items ?? {})) ? vs.map(Number) : vs)} />;
  }
  if (choices && !isFile(schema)) {
    const data = choices.map((c) => ({ value: String(c.value), label: c.label }));
    return <Select {...common} searchable clearable data={data} value={value === undefined || value === '' ? null : String(value)}
      onChange={(v) => onValue(v === null ? undefined : choices.find((c) => String(c.value) === v)?.value)} />;
  }
  if (isFile(schema)) {
    const many = t === 'array';
    return many
      ? <FileInput {...common} multiple clearable value={files} onChange={(f) => onFiles(f ?? [])} />
      : <FileInput {...common} clearable value={files[0] ?? null} onChange={(f) => onFiles(f ? [f] : [])} />;
  }
  if (schema.enum) {
    return <Select {...common} clearable data={schema.enum.map(String)} value={value === undefined ? null : String(value)}
      onChange={(v) => onValue(v === null ? undefined : schema.enum!.find((e) => String(e) === v))} />;
  }
  switch (t) {
    case 'boolean':
      return <Switch label={label} description={schema.description} disabled={readOnly} data-field={name} checked={value === true}
        onChange={(e) => onValue(e.currentTarget.checked)} />;
    case 'integer':
    case 'number':
      return <NumberInput {...common} allowDecimal={t === 'number'} value={typeof value === 'number' ? value : ''}
        onChange={(v) => onValue(typeof v === 'number' ? v : v === '' ? undefined : Number(v))} />;
    case 'array': {
      const items = schema.items ?? {};
      const list = Array.isArray(value) ? value.map(String) : [];
      const back = (vs: string[]) => onValue(['integer', 'number'].includes(kind(items)) ? vs.map(Number) : vs);
      return items.enum
        ? <MultiSelect {...common} data={items.enum.map(String)} value={list} onChange={back} />
        : <TagsInput {...common} value={list} onChange={back} />;
    }
    case 'string':
      if (schema.writeOnly) {
        return <PasswordInput {...common} autoComplete="off" value={text(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
      }
      if (schema.format === 'text') {
        return <Textarea {...common} autosize minRows={2} value={text(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
      }
      if (schema.format === 'date' || schema.format === 'date-time') {
        const local = schema.format === 'date-time' && typeof value === 'string' && value ? toLocal(value) : text(value);
        return <TextInput {...common} type={schema.format === 'date' ? 'date' : 'datetime-local'} value={local}
          onChange={(e) => onValue(schema.format === 'date' ? e.currentTarget.value : fromLocal(e.currentTarget.value))} />;
      }
      return <TextInput {...common} value={text(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
  }
  return <Textarea {...common} autosize minRows={2} value={typeof value === 'string' ? value : text(value)}
    description={schema.description ?? 'JSON'} onChange={(e) => onValue(e.currentTarget.value)} />;
}
