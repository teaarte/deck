const params = new URLSearchParams(location.search);
const fromUrl = params.get('token');
if (fromUrl) {
  localStorage.setItem('deck-token', fromUrl);
  params.delete('token');
  history.replaceState(null, '', location.pathname + (params.size ? '?' + params : '') + location.hash);
}

export const token = (): string => localStorage.getItem('deck-token') ?? '';
export const setToken = (t: string) => localStorage.setItem('deck-token', t);

export class CommandError extends Error {}

// call invokes a registry command and returns its JSON result; a refusal throws the server's text.
export async function call<T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<T> {
  const r = await fetch('/api/commands/' + encodeURIComponent(command), {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + token(), 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  const text = await r.text();
  if (r.status === 401) {
    localStorage.removeItem('deck-token');
    location.reload();
  }
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

export interface Uploaded {
  id: number;
  name: string;
}

// upload sends files to a project; commands then take them by id.
export async function upload(project: string, files: File[]): Promise<Uploaded[]> {
  const body = new FormData();
  files.forEach((f) => body.append('file', f, f.name));
  const r = await fetch('/api/files?project=' + encodeURIComponent(project), {
    method: 'POST', headers: { Authorization: 'Bearer ' + token() }, body,
  });
  const text = await r.text();
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return JSON.parse(text) as Uploaded[];
}

// download saves a stored file through the browser; the token goes in the header, not in the address.
export async function download(id: number): Promise<void> {
  const r = await fetch('/api/files/' + id, { headers: { Authorization: 'Bearer ' + token() } });
  if (!r.ok) throw new CommandError((await r.text()).trim() || r.statusText);
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([await r.arrayBuffer()], { type: 'application/octet-stream' }));
  a.download = fileName(r.headers.get('Content-Disposition') ?? '');
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 60000);
}

// fileName reads the name of Content-Disposition: percent-encoded in filename*, quoted or bare in filename.
function fileName(disposition: string): string {
  const encoded = /filename\*=utf-8''([^;]+)/i.exec(disposition);
  if (encoded) return decodeURIComponent(encoded[1]);
  const quoted = /filename="((?:[^"\\]|\\.)*)"/i.exec(disposition);
  if (quoted) return quoted[1].replace(/\\(.)/g, '$1');
  return /filename=([^;]+)/i.exec(disposition)?.[1].trim() ?? 'file';
}
