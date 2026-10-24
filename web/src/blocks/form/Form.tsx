import { useContext, useRef, useState } from 'react';
import { Alert, Button, Group, Loader, Select, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { DialogClose } from '../../screen/dialogContext';
import { outcome } from '../../screen/act';
import { openTopic } from '../../chat/chatStore';
import { press, useBlockContext } from '../../screen/press';
import { sharedSent } from '../../lib/share';
import { errorText } from '../../lib/notify';
import { Failed } from '../../ui/actions/Failed';
import type { FormValue as Generated } from '../../lib/catalog.gen';
import { useMountedRef } from '../../hooks/useMountedRef';
import { Field } from './Field';
import { Result } from './Result';
import { buildInput, type Schema } from './schema';
import { useFormValues } from './useFormValues';
import { FINAL, useProcessWatch, type ProcessState } from './useProcessWatch';

type FormCommand = Omit<Generated['commands'][number], 'input' | 'result'> & { input?: Schema; result?: Schema };
export type FormValue = Omit<Generated, 'commands'> & { commands: FormCommand[] };

type Outcome = ProcessState & { process?: number };

// Form asks for the input of a command by its schema, calls it and shows the result under the form.
export function Form({ value }: { value: FormValue }) {
  const { t } = useTranslation();
  const inDialog = useContext(DialogClose);
  const close = value.keep_open ? undefined : inDialog;
  const [picked, setPicked] = useState(value.commands.length === 1 ? value.commands[0].name : '');
  const cmd = value.commands.find((c) => c.name === picked);
  const alive = useMountedRef();
  const ctx = useBlockContext();
  const form = useFormValues(value.values as Record<string, unknown> | undefined, value.record, cmd, alive);
  const watch = useProcessWatch(alive);
  const [busy, setBusy] = useState(false);
  const [out, setOut] = useState<Outcome>();
  // failed is why the press of run or keep did not go through.
  const [failed, setFailed] = useState({ at: '', error: '' });
  const running = useRef(false);
  const props = cmd?.input?.properties ?? {};
  const fields = [...(cmd?.order ?? []).filter((k) => k in props), ...Object.keys(props).filter((k) => !cmd?.order?.includes(k))];
  const required = new Set(cmd?.input?.required ?? []);
  const ro = new Set(value.readonly ?? []);
  const edit = !!value.send?.input;
  const project = cmd?.project ?? value.project;

  const submit = async () => {
    if (!cmd || running.current) return;
    running.current = true;
    setFailed({ at: '', error: '' });
    setBusy(true);
    setOut(undefined);
    try {
      // The fixed fields are not drawn; their values go as the server gave them.
      const data = { ...await buildInput(props, form.values, form.files,
        { project: project ?? '', record: edit ? (value.values as Record<string, unknown> | undefined) ?? {} : undefined }), ...value.fixed };
      const sent = value.send?.input
        ? await press(ctx, value.send, { [value.send.input]: JSON.stringify(data) })
        : await press(ctx, { command: cmd.name }, data);
      if (sent.error) {
        setFailed({ at: 'run', error: sent.error });
        return;
      }
      const res = sent.result;
      const hidden = Object.keys(props).filter((k) => props[k].writeOnly);
      if (hidden.length && alive.current) form.forget(hidden);
      if (Object.values(form.files).some((f) => f.length)) await sharedSent();
      const { started, topic } = outcome(res);
      // A command that answers with a chat topic, such as one telling the master, opens it.
      if (topic) openTopic(topic);
      if (!started) {
        if (alive.current) setOut({ state: 'done', result: res });
        close?.();
        return;
      }
      // In a dialog the dialog closes on a started process; the cockpit and the process page follow it.
      if (close) {
        close();
        return;
      }
      await watch(started, (p) => setOut({ ...p, process: started }));
    } catch (e) {
      setFailed({ at: 'run', error: errorText(e) });
    } finally {
      running.current = false;
      if (alive.current) setBusy(false);
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
          <Failed error={failed.at === 'run' ? failed.error : ''} onClose={() => setFailed({ at: '', error: '' })}>
            <Button onClick={submit} loading={busy} data-submit>{edit ? t('form.save_record') : t('form.run')}</Button>
          </Failed>
        </Group>
      )}
      {out && (
        <Stack gap="xs" data-outcome={out.state}>
          {!FINAL.includes(out.state) && <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">{t('form.state', { id: out.process, state: out.state })}</Text></Group>}
          {out.state === 'done' && <Result value={out.result} schema={edit ? undefined : cmd?.result} />}
          {out.state !== 'done' && FINAL.includes(out.state) && <Alert color="red" title={out.state}>{out.error}</Alert>}
        </Stack>
      )}
    </Stack>
  );
}
