import { useEffect, useRef, useState } from 'react';
import { useComputedColorScheme, useMantineTheme } from '@mantine/core';
import { AppBridge, PostMessageTransport, type McpUiHostContext } from '@modelcontextprotocol/ext-apps/app-bridge';
import type { CallToolResult } from '@modelcontextprotocol/sdk/types.js';
import { call } from '../../lib/api';
import type { CustomValue } from '../../lib/catalog.gen';
import { hostContext } from './hostContext';

type CustomButton = NonNullable<CustomValue['actions']>[number];
export type Ask = { act: CustomButton; input: Record<string, unknown> };

// button is the one of the block's buttons a call means: its command, and fixed params the call does not contradict.
function button(actions: CustomButton[], name: string, args: Record<string, unknown>): CustomButton | undefined {
  return actions.find((a) => a.command === name &&
    Object.entries(a.params ?? {}).every(([k, v]) => args[k] === undefined || String(args[k]) === String(v)));
}

const PROXY_READY = 'ui/notifications/sandbox-proxy-ready';

const text = (s: string): CallToolResult['content'] => [{ type: 'text', text: s }];

// useAppBridge connects the frame of a custom block to deck: the theme, the data of its source, its size, and calls
// of its buttons' commands, those outside deck asked of the owner first.
export function useAppBridge(value: CustomValue, ask: (q: Ask) => Promise<boolean>) {
  const frame = useRef<HTMLIFrameElement>(null);
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

  return { frame, height };
}
