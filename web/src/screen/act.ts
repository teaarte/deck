import { call } from '../lib/api';
import i18n from '../lib/i18n';
import { notifyDone, notifyError } from '../lib/notify';
import type { Action } from '../lib/catalog.gen';
import { openTopic } from '../chat/chatStore';
import { go } from '../router';

// summary is the line a command result shows as: a started process, its result or text, or done.
export function summary(out: unknown): string {
  if (out && typeof out === 'object') {
    const o = out as Record<string, unknown>;
    if (typeof o.process === 'number') return i18n.t('started', { id: o.process });
    const rest = Object.fromEntries(Object.entries(o).filter(([k]) => k !== 'ok'));
    const shown = 'result' in rest ? rest.result : typeof rest.text === 'string' ? rest.text : undefined;
    if (shown === undefined || shown === null) return i18n.t('done');
    if (typeof shown === 'string') return shown;
    const text = JSON.stringify(shown);
    return text.length > 200 ? text.slice(0, 200) + '…' : text;
  }
  return typeof out === 'string' && out ? out : i18n.t('done');
}

// act runs a button: a move to its screen, or its call with the outcome told; a chat button opens the topic it made.
// It says whether the call went through.
export async function act(c: Action): Promise<boolean> {
  if (c.screen) {
    const params = c.params as Record<string, string> | undefined;
    go({ project: c.project || undefined, screen: c.screen, record: c.record, params: params && Object.keys(params).length ? params : undefined });
    return true;
  }
  try {
    const out = await call(c.command!, c.params ?? {});
    const topic = (out as { topic?: unknown } | null)?.topic;
    if (c.chat && typeof topic === 'number') openTopic(topic);
    else notifyDone(summary(out));
    return true;
  } catch (e) {
    notifyError(e, c.label || i18n.t('verb.' + (c.verb ?? '')));
    return false;
  }
}
