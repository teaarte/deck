import { useSyncExternalStore } from 'react';
import { call, upload } from '../lib/api';
import { errorText } from '../lib/notify';
import { fromFile, record, type Recording } from './voice';

// VoiceState is the voice of the owner: seconds of the recording going on from 1, pieces being turned into text,
// and why the last ones were not.
export interface VoiceState { seconds: number; hearing: boolean; failed?: string }

// A piece of a recording is kept until it is text; the text of a take goes to a composer once every piece is.
interface Piece { wav: Blob; text?: string; error?: string }

let state: VoiceState = { seconds: 0, hearing: false };
let rec: Recording | undefined;
let timer: ReturnType<typeof setInterval> | undefined;
let pieces: Piece[] = [];
let waiting = '';
const hearers: ((text: string) => void)[] = [];
const subscribers = new Set<() => void>();

const set = (s: Partial<VoiceState>) => {
  state = { ...state, ...s };
  subscribers.forEach((f) => f());
};

// useVoiceState follows the voice of the owner.
export const useVoiceState = () => useSyncExternalStore((f) => { subscribers.add(f); return () => subscribers.delete(f); }, () => state);

// hearBy hands the text of the voice to the composer that came last, the text none took first; once the last
// composer goes the recording stops, and its text waits for the next.
export function hearBy(f: (text: string) => void): () => void {
  hearers.push(f);
  if (waiting) { f(waiting); waiting = ''; }
  return () => {
    hearers.splice(hearers.indexOf(f), 1);
    if (!hearers.length && rec) {
      try { stop(); } catch { /* nothing was recorded */ }
    }
  };
}

// listen starts the microphone; each piece of the recording turns into text while it goes on.
export async function listen() {
  rec = await record((wav) => { pieces.push({ wav }); void pump(); });
  set({ seconds: 1, failed: undefined });
  timer = setInterval(() => set({ seconds: state.seconds + 1 }), 1000);
}

// stop ends the recording; its last piece turns into text with the rest.
export function stop() {
  const r = rec;
  rec = undefined;
  clearInterval(timer);
  set({ seconds: 0 });
  try {
    r?.stop();
  } finally {
    void pump();
  }
}

// take turns a recording of a file into text, in pieces like one of the microphone.
export async function take(file: File) {
  pieces.push(...(await fromFile(file)).map((wav) => ({ wav })));
  set({ failed: undefined });
  await pump();
}

// again turns the pieces that failed into text once more.
export async function again() {
  pieces.forEach((p) => { p.error = undefined; });
  set({ failed: undefined });
  await pump();
}

// discard drops the pieces of a recording that did not turn into text.
export function discard() {
  pieces = [];
  set({ failed: undefined });
}

let pumping = false;

// pump turns the pieces into text one by one, in order; once the recording ended and every piece is text, the text
// goes to the composer.
async function pump() {
  if (pumping) return;
  pumping = true;
  set({ hearing: true });
  try {
    for (let p = pieces.find((x) => x.text === undefined && !x.error); p; p = pieces.find((x) => x.text === undefined && !x.error)) {
      try {
        const [f] = await upload('master', [new File([p.wav], 'voice.wav', { type: 'audio/wav' })]);
        p.text = (await call<{ text: string }>('deck.transcribe', { file: String(f.id) })).text;
      } catch (e) {
        p.error = errorText(e);
      }
    }
  } finally {
    pumping = false;
    set({ hearing: false });
  }
  if (rec) return;
  const failed = pieces.find((x) => x.error);
  if (failed) {
    set({ failed: failed.error });
    return;
  }
  const text = pieces.map((x) => x.text?.trim()).filter(Boolean).join(' ');
  pieces = [];
  if (!text) return;
  if (hearers.length) hearers[hearers.length - 1](text);
  else waiting = waiting ? waiting + ' ' + text : text;
}
