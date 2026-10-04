import { useEffect, useState } from 'react';
import { Anchor, Badge, Button, Code, Collapse, Group, Stack, Text, TextInput, Textarea, Typography } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { renderMarkdown } from '@a2ui/markdown-it';
import { call } from './lib/api';
import type { DocumentValue } from './lib/catalog.gen';

// Markdown draws markdown text as HTML the renderer has sanitized.
export function Markdown({ text }: { text: string }) {
  const [html, setHtml] = useState('');
  useEffect(() => {
    let live = true;
    void renderMarkdown(text).then((h) => { if (live) setHtml(h); });
    return () => { live = false; };
  }, [text]);
  return <Typography data-markdown><div dangerouslySetInnerHTML={{ __html: html }} /></Typography>;
}

// Document shows a document with its revisions; an md one is edited as text and saved as a new revision.
export function Document({ value }: { value: DocumentValue }) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [history, setHistory] = useState(false);
  const md = value.name.endsWith('.md');

  const run = async (command: string, input: Record<string, unknown>, done: string) => {
    setBusy(true);
    try {
      await call(command, input);
      notifications.show({ message: done, color: 'green' });
      return true;
    } catch (e) {
      notifications.show({ title: value.name, message: (e as Error).message, color: 'red', autoClose: 10000 });
      return false;
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    const input: Record<string, unknown> = { project: value.project, name: value.name, kind: value.kind, text: draft, reason };
    if (value.record) input.record = value.record;
    if (value.card) input.card = value.card;
    if (await run('deck.document_save', input, t('document.saved', { name: value.name }))) {
      setEditing(false);
      setReason('');
    }
  };

  return (
    <Stack gap="sm" data-document={value.name}>
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap">
          <Text fw={500} style={{ overflowWrap: 'anywhere' }}>{value.name}</Text>
          <Badge variant="light" size="sm">{value.kind}</Badge>
          {value.revision > 0 && <Text size="xs" c="dimmed">{t('document.revision', { n: value.revision })}</Text>}
        </Group>
        {value.editable && !editing && (
          <Button size="xs" variant="default" onClick={() => { setDraft(value.text); setEditing(true); }} data-edit>{t('document.edit')}</Button>
        )}
      </Group>
      {editing ? (
        <Stack gap="xs">
          <Textarea autosize minRows={8} value={draft} onChange={(e) => setDraft(e.currentTarget.value)} data-draft
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <TextInput size="xs" placeholder={t('document.why')} value={reason} onChange={(e) => setReason(e.currentTarget.value)} data-reason />
          <Group gap="xs">
            <Button size="xs" loading={busy} onClick={save} data-save-document>{t('document.save')}</Button>
            <Button size="xs" variant="subtle" onClick={() => setEditing(false)}>{t('verb.cancel')}</Button>
          </Group>
        </Stack>
      ) : !value.id ? (
        <Text size="sm" c="dimmed">{t('document.none')}</Text>
      ) : md ? (
        <Markdown text={value.text} />
      ) : (
        <Code block data-source>{value.text}</Code>
      )}
      {value.revisions.length > 1 && (
        <Stack gap={4} align="flex-start">
          <Anchor component="button" size="xs" onClick={() => setHistory(!history)} data-history>
            {t('document.history', { count: value.revisions.length })}
          </Anchor>
          <Collapse expanded={history} w="100%">
            <Stack gap={4}>
              {value.revisions.map((r) => (
                <Group key={r.revision} gap="xs" justify="space-between" wrap="nowrap" data-revision={r.revision}>
                  <Text size="xs" style={{ overflowWrap: 'anywhere' }}>
                    #{r.revision} · {r.author} · {new Date(r.created_at).toLocaleString()}{r.reason ? ' · ' + r.reason : ''}
                  </Text>
                  {value.editable && r.revision !== value.revision && (
                    <Button size="compact-xs" variant="default" loading={busy} data-revert={r.revision}
                      onClick={() => void run('deck.document_revert', { document: value.id, revision: r.revision, if_revision: value.revision },
                        t('document.reverted', { n: r.revision }))}>
                      {t('document.revert')}
                    </Button>
                  )}
                </Group>
              ))}
            </Stack>
          </Collapse>
        </Stack>
      )}
    </Stack>
  );
}
