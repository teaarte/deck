import { useEffect, useRef, useState } from 'react';
import { Button, Code, Group, Modal, Stack, Text, useComputedColorScheme, useMantineTheme } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { AppBridge, PostMessageTransport, type McpUiHostContext, type McpUiStyles } from '@modelcontextprotocol/ext-apps/app-bridge';
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

// HOST_VARIABLES are the MCP Apps style variables by the Mantine variable whose resolved value they take.
const HOST_VARIABLES: Partial<Record<keyof McpUiStyles, string>> = {
  '--color-background-primary': '--mantine-color-body',
  '--color-background-secondary': '--mantine-color-default',
  '--color-background-tertiary': '--mantine-color-default-hover',
  '--color-background-inverse': '--mantine-color-text',
  '--color-background-info': '--mantine-primary-color-light',
  '--color-background-danger': '--mantine-color-red-light',
  '--color-background-success': '--mantine-color-green-light',
  '--color-background-warning': '--mantine-color-yellow-light',
  '--color-background-disabled': '--mantine-color-disabled',
  '--color-text-primary': '--mantine-color-text',
  '--color-text-secondary': '--mantine-color-dimmed',
  '--color-text-tertiary': '--mantine-color-placeholder',
  '--color-text-inverse': '--mantine-color-body',
  '--color-text-info': '--mantine-primary-color-light-color',
  '--color-text-danger': '--mantine-color-error',
  '--color-text-success': '--mantine-color-green-light-color',
  '--color-text-warning': '--mantine-color-yellow-light-color',
  '--color-text-disabled': '--mantine-color-disabled-color',
  '--color-border-primary': '--mantine-color-default-border',
  '--color-border-secondary': '--mantine-color-default-border',
  '--color-border-info': '--mantine-primary-color-filled',
  '--color-border-danger': '--mantine-color-error',
  '--color-ring-primary': '--mantine-primary-color-filled',
  '--font-sans': '--mantine-font-family',
  '--font-mono': '--mantine-font-family-monospace',
  '--font-text-xs-size': '--mantine-font-size-xs',
  '--font-text-sm-size': '--mantine-font-size-sm',
  '--font-text-md-size': '--mantine-font-size-md',
  '--font-text-lg-size': '--mantine-font-size-lg',
  '--font-heading-sm-size': '--mantine-h4-font-size',
  '--font-heading-md-size': '--mantine-h3-font-size',
  '--font-heading-lg-size': '--mantine-h2-font-size',
  '--font-heading-xl-size': '--mantine-h1-font-size',
  '--border-radius-xs': '--mantine-radius-xs',
  '--border-radius-sm': '--mantine-radius-sm',
  '--border-radius-md': '--mantine-radius-md',
  '--border-radius-lg': '--mantine-radius-lg',
  '--border-radius-xl': '--mantine-radius-xl',
  '--shadow-sm': '--mantine-shadow-sm',
  '--shadow-md': '--mantine-shadow-md',
  '--shadow-lg': '--mantine-shadow-lg',
};

// hostContext is the theme of deck as MCP Apps passes it: resolved values, since the frame has none of deck's CSS.
function hostContext(theme: 'light' | 'dark'): McpUiHostContext {
  const css = getComputedStyle(document.documentElement);
  const variables = Object.fromEntries(Object.entries(HOST_VARIABLES)
    .map(([k, from]) => [k, css.getPropertyValue(from!).trim()]).filter(([, v]) => v)) as McpUiStyles;
  return { theme, styles: { variables }, displayMode: 'inline', platform: 'web' };
}

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
  const scheme = useComputedColorScheme('light');
  const mantine = useMantineTheme();
  const context = useRef<McpUiHostContext>(undefined);
  // The frame hears of a new theme in place, the way it hears of new data.
  useEffect(() => {
    context.current = hostContext(scheme);
    if (ready) bridge.current?.setHostContext(context.current);
  }, [scheme, mantine, ready]);

  useEffect(() => {
    const iframe = frame.current!;
    const b = new AppBridge(null, { name: 'deck', version: '1' }, { serverTools: {} }, { hostContext: context.current });
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
