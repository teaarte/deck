import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { press, useBlockContext } from '../screen/press';
import { notifyError } from '../lib/notify';
import type { ChatData } from './chatData';
import { chatScreen } from './chatStore';

// newKey is a retry key of a draft; it works on plain http, where randomUUID does not.
function newKey(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
}

// useChatSend sends a message once at a time with the screen of the owner unless detached; a retry of the same draft
// keeps its key, so the server takes it once.
export function useChatSend(value: ChatData, { before, files, model, sent }: {
  before: () => void; files: () => Promise<string[] | undefined>; model: () => string; sent: (topic: number) => void;
}) {
  const { t } = useTranslation();
  const ctx = useBlockContext();
  const [busy, setBusy] = useState(false);
  const key = useRef('');
  const send = async (text: string, detached: boolean) => {
    const message = text.trim();
    if (!message || busy) return;
    setBusy(true);
    before();
    key.current ||= newKey();
    try {
      const ids = await files();
      const r = await press(ctx, value.send, {
        text: message, ...(detached ? {} : { screen: chatScreen() }), retry_key: key.current, ...(ids ? { files: ids } : {}), ...(model() ? { model: model() } : {}),
      });
      if (r.error !== undefined) throw new Error(r.error);
      key.current = '';
      sent((r.result as { topic: number }).topic);
    } catch (e) {
      notifyError(e, t('chat.send_failed'));
    } finally {
      setBusy(false);
    }
  };
  return { busy, send, forgetKey: () => { key.current = ''; } };
}
