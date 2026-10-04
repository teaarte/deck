import { useContext, useRef, useState } from 'react';
import { Alert, Button, Group, Loader, Select, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { DialogClose } from '../../dialog';
import { call } from '../../lib/api';
import { sharedSent } from '../../lib/share';
import { notifyDone, notifyError } from '../../lib/notify';
import type { FormValue as Generated } from '../../lib/catalog.gen';
import { useMountedRef } from '../../hooks/useMountedRef';
import { Field } from './Field';
import { Result } from './Result';
import { buildInput, type Schema } from './schema';
import { useFormValues } from './useFormValues';
import { FINAL, useProcessWatch, type ProcessState } from './useProcessWatch';

type FormCommand = Omit<Generated['commands'][number], 'input' | 'result'> & { input?: Schema; result?: Schema };
export type FormValue = Omit<Generated, 'commands'> & { commands: FormCommand[] };

const EDIT = 'deck.record_update';

type Outcome = ProcessState & { process?: number };

// Form asks for the input of a command by its schema, calls it and shows the result under the form.
export function Form({ value }: { value: FormValue }) {
  const { t } = useTranslation();
  const inDialog = useContext(DialogClose);
  const close = value.keep_open ? undefined : inDialog;
  const [picked, setPicked] = useState(value.commands.length === 1 ? value.commands[0].name : '');
  const cmd = value.commands.find((c) => c.name === picked);
  const alive = useMountedRef();
  const form = useFormValues(value.values as Record<string, unknown> | undefined, value.record, cmd, alive);
  const watch = useProcessWatch(alive);
  const [busy, setBusy] = useState(false);
  const [out, setOut] = useState<Outcome>();
  const running = useRef(false);
  const props = cmd?.input?.properties ?? {};
  const fields = [...(cmd?.order ?? []).filter((k) => k in props), ...Object.keys(props).filter((k) => !cmd?.order?.includes(k))];
  const required = new Set(cmd?.input?.required ?? []);
  const ro = new Set(value.readonly ?? []);
  const edit = cmd?.name === EDIT;
  const project = cmd?.project ?? value.project;

  const submit = async () => {
    if (!cmd || running.current) return;
    running.current = true;
    setBusy(true);
    setOut(undefined);
    try {
      const data = await buildInput(props, form.values, form.files,
        { project: project ?? '', record: edit ? (value.values as Record<string, unknown> | undefined) ?? {} : undefined });
      const res = await call<unknown>(cmd.name, edit ? { record: value.record, data: JSON.stringify(data) } : data);
      const hidden = Object.keys(props).filter((k) => props[k].writeOnly);
      if (hidden.length && alive.current) form.forget(hidden);
      if (Object.values(form.files).some((f) => f.length)) await sharedSent();
      const started = res && typeof res === 'object' && typeof (res as { process?: unknown }).process === 'number'
        && Object.keys(res).length === 1 ? (res as { process: number }).process : 0;
      if (!started) {
        if (alive.current) setOut({ state: 'done', result: res });
        close?.();
        return;
      }
      // In a dialog a started process is told about and the dialog closes; the process page follows it.
      if (close) {
        notifyDone(t('started', { id: started }));
        close();
        return;
      }
      await watch(started, (p) => setOut({ ...p, process: started }));
    } catch (e) {
      notifyError(e, cmd.title);
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
      notifyDone(t('form.saved', { collection: value.save }));
    } catch (e) {
      notifyError(e);
    }
  };

  return (
    <Stack gap="sm" data-form={cmd?.name ?? ''}>
      {value.commands.length > 1 && (
        <Select label={t('form.command')} data={value.commands.map((c) => ({ value: c.name, label: `${c.title} (${c.name})` }))}
          value={picked || null} onChange={(v) => { setPicked(v ?? ''); form.reset(); setOut(undefined); }} data-pick />
      )}
      {cmd?.description && <Text size="sm" c="dimmed">{cmd.description}</Text>}
      {cmd && fields.map((name) => [name, props[name]] as const).map(([name, s]) => (
        <Field key={cmd.name + name} name={name} schema={s} value={form.values[name]} files={form.files[name] ?? []}
          readOnly={ro.has(name)} required={required.has(name) && !edit} choices={value.choices?.[name]}
          onValue={(v) => form.edit(name, v)} onFiles={(f) => form.attach(name, f)} />
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
