import { useState } from 'react';
import { Anchor, Badge, Button, Code, Collapse, Group, Stack, Text, TextInput, Textarea } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action, DocumentValue } from '../lib/catalog.gen';
import { Markdown } from '../ui/Markdown';
import { blockImpl } from './blockImpl';
import { Failed } from '../ui/actions/Failed';
import { usePress } from '../ui/actions/usePress';
import { locale } from '../lib/i18n';

const none: Action = {};

// Revert is the button of a revision that goes back to it.
function Revert({ act, revision }: { act: Action; revision: number }) {
  const { t } = useTranslation();
  const { busy, send, error, clear } = usePress(act);
  return (
    <Failed error={error} onClose={clear}>
      <Button size="compact-xs" variant="default" loading={busy} data-revert={revision} onClick={() => void send()}>{t('document.revert')}</Button>
    </Failed>
  );
}

// Document shows a document with its revisions; an md one is edited as text and saved as a new revision by the
// buttons the server gives it.
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

  const send = async () => {
    const r = await save.send({ text: draft, reason, ...(base > 0 ? { if_revision: base } : {}) });
    if (r && !r.error) {
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
        {value.save && !editing && (
          <Button size="xs" variant="default" onClick={() => { setDraft(value.text); setBase(value.revision); setEditing(true); }} data-edit>{t('document.edit')}</Button>
        )}
      </Group>
      {editing ? (
        <Stack gap="xs">
          <Textarea autosize minRows={8} value={draft} onChange={(e) => setDraft(e.currentTarget.value)} data-draft
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
          <TextInput size="xs" placeholder={t('document.why')} value={reason} onChange={(e) => setReason(e.currentTarget.value)} data-reason />
          <Group gap="xs">
            <Failed error={save.error} onClose={save.clear}>
              <Button size="xs" loading={save.busy} onClick={send} data-save-document>{t('document.save')}</Button>
            </Failed>
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
