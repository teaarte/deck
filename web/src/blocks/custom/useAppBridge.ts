import { useEffect, useRef, useState } from 'react';
import { useComputedColorScheme, useMantineTheme } from '@mantine/core';
import { AppBridge, PostMessageTransport, type McpUiHostContext } from '@modelcontextprotocol/ext-apps/app-bridge';
import type { CallToolResult, ReadResourceResult } from '@modelcontextprotocol/sdk/types.js';
import { call } from '../../lib/api';
import { go, parse } from '../../app/router';
import type { CustomValue } from '../../lib/catalog.gen';
import { hostContext } from './hostContext';

type CustomButton = NonNullable<CustomValue['actions']>[number];
export type Ask = { act: CustomButton; input: Record<string, unknown> };

// button is the one of the block's buttons a call means: its command, fixed params the call does not contradict, and
// no field but those params and the ones the block fills, a record only one the block was given.
function button(value: CustomValue, name: string, args: Record<string, unknown>): CustomButton | undefined {
  const ids = new Set([...(value.items ?? []), ...(value.record ? [value.record] : [])].map((it) => String((it as { _id?: unknown })._id)));
  return (value.actions ?? []).find((a) => a.command === name &&
    Object.entries(a.params ?? {}).every(([k, v]) => args[k] === undefined || String(args[k]) === String(v)) &&
    Object.keys(args).every((k) => Object.hasOwn(a.params ?? {}, k) || (a.from_block ?? []).includes(k)) &&
    (a.from_items ?? []).every((k) => args[k] === undefined || ids.has(String(args[k]))));
}

const PROXY_READY = 'ui/notifications/sandbox-proxy-ready';

const text = (s: string): CallToolResult['content'] => [{ type: 'text', text: s }];

type Resource = { uri: string; type: string; blob: string };

// useAppBridge connects the frame of a custom block to deck: the theme, the data of its source and its params, its
// size, the files of its module, and calls of its buttons' commands, all but internal ones asked of the owner one by one.
export function useAppBridge(value: CustomValue, ask: (q: Ask) => Promise<boolean>) {
  const frame = useRef<HTMLIFrameElement>(null);
  const bridge = useRef<AppBridge>(undefined);
  const latest = useRef(value);
  const asking = useRef(false);
  latest.current = value;
  const [ready, setReady] = useState(false);
  const [height, setHeight] = useState(240);
  const [full, setFull] = useState(false);
  const [failed, setFailed] = useState('');
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
    const b = new AppBridge(null, { name: 'deck', version: '1' }, { serverTools: {}, serverResources: {}, openLinks: {} }, { hostContext: context.current });
    // A link opens a screen or window of deck; a page outside would carry the data of the frame out.
    b.onopenlink = async ({ url }) => {
      const at = url.startsWith('#/') ? url : URL.canParse(url) && new URL(url).origin === location.origin ? new URL(url).hash : '';
      if (!at.startsWith('#/')) return { isError: true };
      go(parse(at));
      return {};
    };
    b.onrequestdisplaymode = async ({ mode }) => {
      const to = mode === 'fullscreen' ? 'fullscreen' : 'inline';
      setFull(to === 'fullscreen');
      context.current = { ...context.current, displayMode: to,
        containerDimensions: to === 'fullscreen' ? { width: innerWidth, height: innerHeight } : undefined };
      b.setHostContext(context.current);
      return { mode: to };
    };
    b.oncalltool = async (p): Promise<CallToolResult> => {
      const args = (p.arguments ?? {}) as Record<string, unknown>;
      const act = button(latest.current, p.name, args);
      if (!act?.command) {
        const allowed = (latest.current.actions ?? []).filter((a) => a.command)
          .map((a) => `${a.command}(${[...Object.keys(a.params ?? {}), ...(a.from_block ?? [])].join(', ')})`).join(', ') || 'none';
        return { isError: true, content: text(`deck: this block may call only its buttons' commands with their fields: ${allowed}`) };
      }
      const input = { ...args, ...(act.params ?? {}) };
      if (act.class !== 'internal') {
        if (asking.current) return { isError: true, content: text('deck: another call of this block waits for the owner') };
        asking.current = true;
        const ok = await ask({ act, input }).finally(() => { asking.current = false; });
        if (!ok) return { isError: true, content: text(`deck: the owner declined ${act.command}`) };
      }
      try {
        const out = await call(act.command, input);
        const structured = out && typeof out === 'object' && !Array.isArray(out) ? (out as Record<string, unknown>) : { result: out };
        return { content: text(JSON.stringify(out)), structuredContent: structured };
      } catch (e) {
        return { isError: true, content: text((e as Error).message) };
      }
    };
    // A file of the block's module comes whole from deck, which checks it is of that module.
    b.onreadresource = async ({ uri }): Promise<ReadResourceResult> => {
      const project = latest.current.project;
      if (!project) throw new Error('deck: a block on a screen of deck has no module files to read');
      const r = await call<Resource>('deck.file_read', { project, uri });
      return { contents: [{ uri: r.uri, mimeType: r.type, blob: r.blob }] };
    };
    b.onsizechange = ({ height: h }) => { if (h) setHeight(Math.min(Math.max(h, 40), 4000)); };
    b.oninitialized = () => setReady(true);
    b.onloggingmessage = ({ level, logger, data }) => { if (level === 'error' && logger === 'deck') setFailed(String(data)); };
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
      setFailed('');
      void b.close();
    };
  }, [value.html]);

  useEffect(() => {
    if (!ready || !bridge.current) return;
    const data = { items: value.items ?? [], record: value.record ?? null, params: value.params ?? {} };
    void bridge.current.sendToolResult({ content: text(JSON.stringify(data)), structuredContent: data });
  }, [ready, value.items, value.record, value.params]);

  const inline = () => {
    setFull(false);
    context.current = { ...context.current, displayMode: 'inline', containerDimensions: undefined };
    bridge.current?.setHostContext(context.current);
  };
  return { frame, height, full, inline, failed };
}
