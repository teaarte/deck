import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { press, useBlockContext } from '../screen/press';
import { newKey } from '../lib/api';
import { notifyError } from '../lib/notify';
import type { ChatData } from './chatData';
import { chatScreen } from './chatStore';

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
        text: message, ...(detached ? {} : { screen: chatScreen() }), ...(ids ? { files: ids } : {}), ...(model() ? { model: model() } : {}),
      }, key.current);
      if (r.error !== undefined) throw new Error(r.error);
      key.current = '';
      sent((r.result as { topic: number }).topic);
    } catch (e) {
      notifyError(e, t('The message was not sent'));
    } finally {
      setBusy(false);
    }
  };
  return { busy, send, forgetKey: () => { key.current = ''; } };
}
