import { useEffect, useRef, useState } from 'react';
import {
  Alert, Button, FileInput, Group, Loader, MultiSelect, NumberInput, PasswordInput, Select, Stack, Switch, Table, TagsInput, Text,
  TextInput, Textarea,
} from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { call, events, upload } from './api';
import { sharedFiles, sharedSent } from './share';
import type { FormValue as Generated } from './catalog.gen';

export interface Schema {
  type?: string | string[];
  format?: string;
  enum?: unknown[];
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
  description?: string;
  title?: string;
  writeOnly?: boolean;
}

type FormCommand = Omit<Generated['commands'][number], 'input' | 'result'> & { input?: Schema; result?: Schema };
export type FormValue = Omit<Generated, 'commands'> & { commands: FormCommand[] };

const EDIT = 'deck.record_update';
const FINAL = ['done', 'error', 'interrupted', 'stopped'];

const kind = (s: Schema) => (Array.isArray(s.type) ? s.type.find((x) => x !== 'null') : s.type) ?? '';
const isFile = (s: Schema) => s.format === 'file' || s.items?.format === 'file';
const show = (v: unknown) => (v === null || v === undefined ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v));

type Choice = { value: unknown; label: string };

function Field({ name, schema, value, files, readOnly, required, choices, onValue, onFiles }: {
  name: string; schema: Schema; value: unknown; files: File[]; readOnly: boolean; required: boolean; choices?: Choice[];
  onValue: (v: unknown) => void; onFiles: (f: File[]) => void;
}) {
  const label = schema.title || name;
  const common = { label, description: schema.description, required, disabled: readOnly, 'data-field': name };
  const t = kind(schema);
  // A list takes the values offered and others typed, such as project.*; a single value is one of those offered.
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
        return <PasswordInput {...common} autoComplete="off" value={show(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
      }
      if (schema.format === 'text') {
        return <Textarea {...common} autosize minRows={2} value={show(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
      }
      if (schema.format === 'date' || schema.format === 'date-time') {
        const local = schema.format === 'date-time' && typeof value === 'string' && value ? toLocal(value) : show(value);
        return <TextInput {...common} type={schema.format === 'date' ? 'date' : 'datetime-local'} value={local}
          onChange={(e) => onValue(schema.format === 'date' ? e.currentTarget.value : fromLocal(e.currentTarget.value))} />;
      }
      return <TextInput {...common} value={show(value)} onChange={(e) => onValue(e.currentTarget.value)} />;
  }
  return <Textarea {...common} autosize minRows={2} value={typeof value === 'string' ? value : show(value)}
    description={schema.description ?? 'JSON'} onChange={(e) => onValue(e.currentTarget.value)} />;
}

const toLocal = (iso: string) => {
  const d = new Date(iso);
  return isNaN(+d) ? iso : new Date(+d - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};
const fromLocal = (v: string) => (v ? new Date(v).toISOString() : '');

// Result draws what a command returned by its result schema: a number, a text, a list or a table.
export function Result({ value, schema }: { value: unknown; schema?: Schema }) {
  const { t } = useTranslation();
  if (value === null || value === undefined) return <Text size="sm" c="dimmed">{t('form.nothing')}</Text>;
  const k = schema ? kind(schema) : Array.isArray(value) ? 'array' : typeof value;
  if (k === 'array' && Array.isArray(value)) {
    const objects = value.every((v) => v && typeof v === 'object' && !Array.isArray(v));
    if (!objects) return <Stack gap={2}>{value.map((v, i) => <Text key={i} size="sm">{show(v)}</Text>)}</Stack>;
    const cols = schema?.items?.properties ? Object.keys(schema.items.properties)
      : [...new Set(value.flatMap((v) => Object.keys(v as object)))];
    return (
      <Table.ScrollContainer minWidth={300}>
        <Table striped data-result="table">
          <Table.Thead><Table.Tr>{cols.map((c) => <Table.Th key={c}>{schema?.items?.properties?.[c]?.title || c}</Table.Th>)}</Table.Tr></Table.Thead>
          <Table.Tbody>
            {value.map((row, i) => (
              <Table.Tr key={i}>{cols.map((c) => <Table.Td key={c}>{show((row as Record<string, unknown>)[c])}</Table.Td>)}</Table.Tr>
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
              : <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{show(o[key])}</Text>}
          </Group>
        ))}
      </Stack>
    );
  }
  if (typeof value === 'number') return <Text fz={32} fw={700} data-result="number">{value}</Text>;
  return <Text size="sm" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }} data-result="text">{show(value)}</Text>;
}

interface Outcome {
  state: string;
  result?: unknown;
  error?: string;
  process?: number;
}

// Form asks for the input of a command by its schema, calls it and shows the result under the form.
export function Form({ value }: { value: FormValue }) {
  const { t } = useTranslation();
  const [picked, setPicked] = useState(value.commands.length === 1 ? value.commands[0].name : '');
  const cmd = value.commands.find((c) => c.name === picked);
  // edited are the fields the owner changed; the rest follow the values the server sends now.
  const [edited, setEdited] = useState<Record<string, unknown>>({});
  const values: Record<string, unknown> = { ...(value.values ?? {}), ...edited };
  const [files, setFiles] = useState<Record<string, File[]>>({});
  const [busy, setBusy] = useState(false);
  const [out, setOut] = useState<Outcome>();
  const running = useRef(false);
  const alive = useRef(true);
  useEffect(() => () => { alive.current = false; }, []);
  useEffect(() => { setEdited({}); }, [value.record]);
  // A change the server now holds, a saved one too, stops being the owner's and follows the server again.
  useEffect(() => {
    setEdited((e) => {
      const kept = Object.entries(e).filter(([k, v]) => String(v) !== String((value.values as Record<string, unknown> | undefined)?.[k]));
      return kept.length === Object.keys(e).length ? e : Object.fromEntries(kept);
    });
  }, [value.values]);
  useEffect(() => {
    const field = cmd && Object.entries(cmd.input?.properties ?? {}).find(([, s]) => isFile(s))?.[0];
    if (!field) return;
    void sharedFiles().then((got) => { if (got.length && alive.current) setFiles((f) => (f[field]?.length ? f : { ...f, [field]: got })); });
  }, [picked]);
  const props = cmd?.input?.properties ?? {};
  const fields = [...(cmd?.order ?? []).filter((k) => k in props), ...Object.keys(props).filter((k) => !cmd?.order?.includes(k))];
  const required = new Set(cmd?.input?.required ?? []);
  const ro = new Set(value.readonly ?? []);
  const edit = cmd?.name === EDIT;
  const project = cmd?.project ?? value.project;

  // input builds the call; an edit starts from the record, so fields the form does not show stay as they were.
  const input = async () => {
    const data: Record<string, unknown> = edit
      ? Object.fromEntries(Object.entries(value.values ?? {}).filter(([k]) => !k.startsWith('_'))) : {};
    for (const [name, s] of Object.entries(props)) {
      if (isFile(s)) {
        const fs = files[name] ?? [];
        if (!fs.length) continue;
        const ids = (await upload(project ?? '', fs)).map((f) => String(f.id));
        data[name] = kind(s) === 'array' ? ids : ids[0];
        continue;
      }
      let v = values[name];
      if (v === undefined || v === '') {
        delete data[name];
        continue;
      }
      if (!['string', 'integer', 'number', 'boolean', 'array'].includes(kind(s)) && typeof v === 'string' && !s.enum) {
        try {
          v = JSON.parse(v);
        } catch {
          throw new Error(t('form.json', { field: name }));
        }
      }
      data[name] = v;
    }
    return data;
  };

  const submit = async () => {
    if (!cmd || running.current) return;
    running.current = true;
    setBusy(true);
    setOut(undefined);
    try {
      const data = await input();
      const res = await call<unknown>(cmd.name, edit ? { record: value.record, data: JSON.stringify(data) } : data);
      const hidden = Object.keys(props).filter((k) => props[k].writeOnly);
      if (hidden.length && alive.current) setEdited((o) => Object.fromEntries(Object.entries(o).filter(([k]) => !hidden.includes(k))));
      if (Object.values(files).some((f) => f.length)) await sharedSent();
      const started = res && typeof res === 'object' && typeof (res as { process?: unknown }).process === 'number'
        && Object.keys(res).length === 1 ? (res as { process: number }).process : 0;
      if (!started) {
        if (alive.current) setOut({ state: 'done', result: res });
        return;
      }
      // The process is read again on each processes event, and every 10 s should an event be missed.
      await new Promise<void>((finished, failed) => {
        let over = false;
        const check = async () => {
          try {
            const p = await call<{ state: string; result?: unknown; error?: string }>('deck.process_get', { process: started });
            if (over || !alive.current) return end();
            setOut({ state: p.state, result: p.result, error: p.error, process: started });
            if (FINAL.includes(p.state)) end();
          } catch (e) {
            end(e);
          }
        };
        const timer = setInterval(() => void check(), 10000);
        const off = events((topic) => { if (topic === 'processes') void check(); }, () => {});
        const end = (e?: unknown) => {
          if (over) return;
          over = true;
          clearInterval(timer);
          off();
          if (e) failed(e); else finished();
        };
        void check();
      });
    } catch (e) {
      notifications.show({ title: cmd.title, message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      running.current = false;
      if (alive.current) setBusy(false);
    }
  };

  const save = async () => {
    if (!value.save || !out) return;
    const rows = Array.isArray(out.result) ? out.result : [out.result];
    try {
      await call('deck.records_upsert', { project, collection: value.save, records: rows.map((r) => JSON.stringify(r)).join('\n') });
      notifications.show({ message: t('form.saved', { collection: value.save }), color: 'green' });
    } catch (e) {
      notifications.show({ message: (e as Error).message, color: 'red', autoClose: 10000 });
    }
  };

  return (
    <Stack gap="sm" data-form={cmd?.name ?? ''}>
      {value.commands.length > 1 && (
        <Select label={t('form.command')} data={value.commands.map((c) => ({ value: c.name, label: `${c.title} (${c.name})` }))}
          value={picked || null} onChange={(v) => { setPicked(v ?? ''); setEdited({}); setFiles({}); setOut(undefined); }} data-pick />
      )}
      {cmd?.description && <Text size="sm" c="dimmed">{cmd.description}</Text>}
      {cmd && fields.map((name) => [name, props[name]] as const).map(([name, s]) => (
        <Field key={cmd.name + name} name={name} schema={s} value={values[name]} files={files[name] ?? []}
          readOnly={ro.has(name)} required={required.has(name) && !edit} choices={value.choices?.[name]}
          onValue={(v) => setEdited((o) => ({ ...o, [name]: v }))} onFiles={(f) => setFiles((o) => ({ ...o, [name]: f }))} />
      ))}
      {cmd && (
        <Group>
          <Button onClick={submit} loading={busy} data-submit>{edit ? t('form.save_record') : t('form.run')}</Button>
        </Group>
      )}
      {out && (
        <Stack gap="xs" data-outcome={out.state}>
          {!FINAL.includes(out.state) && <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">{t('form.state', { id: out.process, state: out.state })}</Text></Group>}
          {out.state === 'done' && <Result value={out.result} schema={edit ? undefined : cmd?.result} />}
          {out.state !== 'done' && FINAL.includes(out.state) && <Alert color="red" title={out.state}>{out.error}</Alert>}
          {out.state === 'done' && value.save && out.result !== null && out.result !== undefined && (
            <Group><Button variant="default" size="xs" onClick={save} data-save>{t('form.keep', { collection: value.save })}</Button></Group>
          )}
        </Stack>
      )}
    </Stack>
  );
}
