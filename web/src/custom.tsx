import { useEffect, useRef, useState } from 'react';
import { Button, Code, Group, Modal, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { AppBridge, PostMessageTransport } from '@modelcontextprotocol/ext-apps/app-bridge';
import type { CallToolResult } from '@modelcontextprotocol/sdk/types.js';
import { call } from './api';
import type { CustomValue } from './catalog.gen';

type CustomButton = NonNullable<CustomValue['actions']>[number];

// button is the one of the block's buttons a call means: its command, and fixed params the call does not contradict.
function button(actions: CustomButton[], name: string, args: Record<string, unknown>): CustomButton | undefined {
  return actions.find((a) => a.command === name &&
    Object.entries(a.params ?? {}).every(([k, v]) => args[k] === undefined || String(args[k]) === String(v)));
}

const PROXY_READY = 'ui/notifications/sandbox-proxy-ready';

const text = (s: string): CallToolResult['content'] => [{ type: 'text', text: s }];

// Custom hosts an MCP App written by a model: its HTML runs in a frame of the second origin without network,
// gets the data of its source as a tool result and may call only the commands of its buttons.
export function Custom({ value }: { value: CustomValue }) {
  const { t } = useTranslation();
  const frame = useRef<HTMLIFrameElement>(null);
  const [asking, setAsking] = useState<{ act: CustomButton; input: Record<string, unknown>; done: (ok: boolean) => void }>();
  const ask = (q: { act: CustomButton; input: Record<string, unknown> }) => new Promise<boolean>((done) => setAsking({ ...q, done }));
  const answer = (ok: boolean) => { asking?.done(ok); setAsking(undefined); };
  const bridge = useRef<AppBridge>(undefined);
  const latest = useRef(value);
  latest.current = value;
  const [ready, setReady] = useState(false);
  const [height, setHeight] = useState(240);

  useEffect(() => {
    const iframe = frame.current!;
    const b = new AppBridge(null, { name: 'deck', version: '1' }, { serverTools: {} });
    b.oncalltool = async (p): Promise<CallToolResult> => {
      const args = (p.arguments ?? {}) as Record<string, unknown>;
      const act = button(latest.current.actions ?? [], p.name, args);
      if (!act?.command) {
        const allowed = (latest.current.actions ?? []).map((a) => a.command).filter(Boolean).join(', ') || 'none';
        return { isError: true, content: text(`deck: this block may call only its buttons' commands: ${allowed}`) };
      }
      const input = { ...args, ...(act.params ?? {}) };
      if (act.class && act.class !== 'internal' && !(await ask({ act, input }))) {
        return { isError: true, content: text(`deck: the owner declined ${act.command}`) };
      }
      try {
        const out = await call(act.command, input);
        const structured = out && typeof out === 'object' && !Array.isArray(out) ? (out as Record<string, unknown>) : { result: out };
        return { content: text(JSON.stringify(out)), structuredContent: structured };
      } catch (e) {
        return { isError: true, content: text((e as Error).message) };
      }
    };
    b.onsizechange = ({ height: h }) => { if (h) setHeight(Math.min(Math.max(h, 40), 4000)); };
    b.oninitialized = () => setReady(true);
    const onReady = (e: MessageEvent) => {
      if (e.source !== iframe.contentWindow || e.data?.method !== PROXY_READY) return;
      window.removeEventListener('message', onReady);
      void b.connect(new PostMessageTransport(iframe.contentWindow!, iframe.contentWindow!))
        .then(() => b.sendSandboxResourceReady({ html: latest.current.html }));
    };
    window.addEventListener('message', onReady);
    bridge.current = b;
    iframe.src = '/custom';
    return () => {
      window.removeEventListener('message', onReady);
      setReady(false);
      void b.close();
    };
  }, [value.html]);

  useEffect(() => {
    if (!ready || !bridge.current) return;
    const data = { items: value.items ?? [], record: value.record ?? null };
    void bridge.current.sendToolResult({ content: text(JSON.stringify(data)), structuredContent: data });
  }, [ready, value.items, value.record]);

  return (
    <>
      <iframe ref={frame} title="custom" data-custom sandbox="allow-scripts allow-same-origin allow-forms"
        style={{ width: '100%', height, border: 0, display: 'block' }} />
      <Modal opened={!!asking} onClose={() => answer(false)} title={t('custom.ask')}>
        {asking && (
          <Stack gap="sm" data-custom-ask>
            <Text size="sm">{asking.act.label} ({asking.act.command})</Text>
            <Code block>{JSON.stringify(asking.input, null, 2)}</Code>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => answer(false)}>{t('verb.deny')}</Button>
              <Button onClick={() => answer(true)} data-custom-allow>{t('verb.allow')}</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </>
  );
}
