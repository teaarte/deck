const params = new URLSearchParams(location.search);
const fromUrl = params.get('token');
if (fromUrl) {
  localStorage.setItem('deck-token', fromUrl);
  params.delete('token');
  history.replaceState(null, '', location.pathname + (params.size ? '?' + params : '') + location.hash);
}

export const token = (): string => localStorage.getItem('deck-token') ?? '';

export class CommandError extends Error {}

// call invokes a registry command and returns its JSON result; a refusal throws the server's text.
export async function call<T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<T> {
  const r = await fetch('/api/commands/' + encodeURIComponent(command), {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + token(), 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  const text = await r.text();
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return (text ? JSON.parse(text) : null) as T;
}

// events follows the live channel and opens it again when the browser gives up on it, as after a refused reconnect.
export function events(onTopic: (topic: string) => void, onState: (live: boolean) => void): () => void {
  let es: EventSource | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  const open = () => {
    es = new EventSource('/api/events?token=' + encodeURIComponent(token()));
    es.onopen = () => onState(true);
    es.onmessage = (e) => onTopic(String(e.data));
    es.onerror = () => {
      onState(false);
      if (es?.readyState === EventSource.CLOSED && !stopped) retry = setTimeout(open, 3000);
    };
  };
  open();
  return () => {
    stopped = true;
    clearTimeout(retry);
    es?.close();
  };
}
