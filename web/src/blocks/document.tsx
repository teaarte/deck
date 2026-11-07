import { useState } from 'react';
import { ActionIcon, Anchor, Badge, Button, Code, Collapse, Group, Stack, Text, TextInput, Textarea } from '@mantine/core';
import { IconFileText, IconPencil } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action, DocumentValue } from '../lib/catalog.gen';
import { Markdown } from '../ui/Markdown';
import { blockImpl } from './blockImpl';
import { Failed } from '../ui/actions/Failed';
import { usePress } from '../ui/actions/usePress';
import { locale } from '../lib/i18n';
import { usePhone } from '../hooks/usePhone';

const none: Action = {};

// unnoted is markdown without the closed HTML comments of its authors outside code: fenced blocks and code spans keep
// theirs, and an unclosed one stays as it is.
export function unnoted(text: string): string {
  return text.split(/(^```[\s\S]*?^```[^\n]*$)/m).map((part, i) => (i % 2
    ? part
    : part.replace(/(`+)[\s\S]*?\1|<!--[\s\S]*?-->/g, (m) => (m.startsWith('`') ? m : '')))).join('');
}

// Revert is the button of a revision that goes back to it.
function Revert({ act, revision }: { act: Action; revision: number }) {
  const { t } = useTranslation();
  const { busy, send, error, clear } = usePress(act);
  return (
    <Failed error={error} onClose={clear}>
      <Button size="compact-xs" variant="default" loading={busy} data-revert={revision} onClick={() => void send()}>{t('Back to this')}</Button>
    </Failed>
  );
}

// Document shows a document with its revisions: its file name in a head that never crowds, notes of its authors
// (HTML comments) left out; an md one is edited as text and saved as a new revision by the buttons the server gives.
function Document({ value }: { value: DocumentValue }) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  // base is the revision the draft was begun from; a save over a newer one is refused.
  const [base, setBase] = useState(0);
  const [reason, setReason] = useState('');
  const [history, setHistory] = useState(false);
  const save = usePress(value.save ?? none);
  const md = value.name.endsWith('.md');
  const phone = usePhone();
  const file = value.name.split('/').pop() || value.name;
  const edit = () => { setDraft(value.text); setBase(value.revision); setEditing(true); };

  const send = async () => {
    const r = await save.send({ text: draft, reason, ...(base > 0 ? { if_revision: base } : {}) });
    if (r && !r.error) {
      setEditing(false);
      setReason('');
    }
  };

  return (
    <Stack gap="sm" data-document={value.name}>
      <Group justify="space-between" wrap="nowrap" gap="xs" data-document-head>
        <Group gap={6} wrap="nowrap" miw={0}>
          <IconFileText size={16} stroke={1.75} style={{ flex: 'none' }} />
          <Text fw={500} truncate title={value.name} data-document-name>{file}</Text>
          {!md && <Badge variant="light" size="sm" style={{ flex: 'none' }}>{value.kind}</Badge>}
        </Group>
        {value.save && !editing && (phone
          ? <ActionIcon variant="default" size={30} aria-label={t('Edit')} onClick={edit} data-edit><IconPencil size={16} /></ActionIcon>
          : <Button size="xs" variant="default" leftSection={<IconPencil size={14} />} onClick={edit} data-edit>{t('Edit')}</Button>)}
      </Group>
      {editing ? (
        <Stack gap="xs">
          <Textarea autosize minRows={8} value={draft} onChange={(e) => setDraft(e.currentTarget.value)} data-draft
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <TextInput size="xs" placeholder={t('Why it changed (optional)')} value={reason} onChange={(e) => setReason(e.currentTarget.value)} data-reason />
          <Group gap="xs">
            <Failed error={save.error} onClose={save.clear}>
              <Button size="xs" loading={save.busy} onClick={send} data-save-document>{t('Save')}</Button>
            </Failed>
            <Button size="xs" variant="subtle" onClick={() => setEditing(false)}>{t('Cancel')}</Button>
          </Group>
        </Stack>
      ) : !value.id ? (
        <Text size="sm" c="dimmed">{t('Not written yet')}</Text>
      ) : md ? (
        <Markdown text={unnoted(value.text)} />
      ) : (
        <Code block data-source>{value.text}</Code>
      )}
      {value.revisions.length > 1 && (
        <Stack gap={4} align="flex-start">
          <Anchor component="button" size="xs" onClick={() => setHistory(!history)} data-history>
            {t('Revisions: {{count}}', { count: value.revisions.length })} · {t('revision {{n}}', { n: value.revision })}
          </Anchor>
          <Collapse expanded={history} w="100%">
            <Stack gap={4}>
              {value.revisions.map((r) => (
                <Group key={r.revision} gap="xs" justify="space-between" wrap="nowrap" data-revision={r.revision}>
                  <Text size="xs" className="deck-wrap">
                    #{r.revision} · {r.author} · {new Date(r.created_at).toLocaleString(locale())}{r.reason ? ' · ' + r.reason : ''}
                  </Text>
                  {r.revert && <Revert act={r.revert} revision={r.revision} />}
                </Group>
              ))}
            </Stack>
          </Collapse>
        </Stack>
      )}
    </Stack>
  );
}

export const documentBlock = blockImpl('document', (_, d) => <Document value={d.value as DocumentValue} />);
