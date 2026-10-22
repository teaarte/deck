import { call, upload } from '../lib/api';
import { createStore, useStore } from '../lib/store';
import { errorText } from '../lib/notify';
import { fromFile, record, type Recording } from './voice';

// VoiceState is the voice of the owner: seconds of the recording going on from 1, pieces being turned into text,
// and why the last ones were not.
export interface VoiceState { seconds: number; hearing: boolean; failed?: string }

// A piece of a recording is kept until it is text; the text of a recording goes to a composer once every piece is.
interface Piece { wav: Blob; take: number; text?: string; error?: string }

const state = createStore<VoiceState>({ seconds: 0, hearing: false });
let rec: Recording | undefined;
let timer: ReturnType<typeof setInterval> | undefined;
let pieces: Piece[] = [];
let takes = 0;
// current is the recording of the microphone going on.
let current = 0;
let waiting = '';
const hearers: ((text: string) => void)[] = [];

const set = (s: Partial<VoiceState>) => state.set({ ...state.get(), ...s });

// useVoiceState follows the voice of the owner.
export const useVoiceState = () => useStore(state);

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

// listen starts the microphone, unless it is starting or on; each piece of the recording turns into text while it
// goes on. A composer gone while it started stops it at once.
let starting = false;
export async function listen() {
  if (starting || rec) return;
  starting = true;
  try {
    const n = ++takes;
    rec = await record((wav) => { pieces.push({ wav, take: n }); void pump(); });
    current = n;
  } finally {
    starting = false;
  }
  set({ seconds: 1 });
  clearInterval(timer);
  timer = setInterval(() => set({ seconds: state.get().seconds + 1 }), 1000);
  if (!hearers.length) {
    try { stop(); } catch { /* nothing was recorded */ }
  }
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
  const n = ++takes;
  pieces.push(...(await fromFile(file)).map((wav) => ({ wav, take: n })));
  await pump();
}

// again turns the pieces that failed into text once more.
export async function again() {
  pieces.forEach((p) => { p.error = undefined; });
  set({ failed: undefined });
  await pump();
}

// discard drops the recordings that did not turn into text, every piece of each.
export function discard() {
  const failed = new Set(pieces.filter((p) => p.error).map((p) => p.take));
  pieces = pieces.filter((p) => !failed.has(p.take));
  set({ failed: undefined });
}

let pumping = false;

// pump turns the pieces into text one by one, in order; a recording that ended with every piece text goes to the
// composer, one that failed stays with why.
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
  const going = rec ? current : 0;
  for (const n of new Set(pieces.map((p) => p.take))) {
    const of = pieces.filter((p) => p.take === n);
    if (n === going || of.some((p) => p.text === undefined)) continue;
    pieces = pieces.filter((p) => p.take !== n);
    const text = of.map((x) => x.text?.trim()).filter(Boolean).join(' ');
    if (!text) continue;
    if (hearers.length) hearers[hearers.length - 1](text);
    else waiting = waiting ? waiting + ' ' + text : text;
  }
  set({ failed: pieces.find((x) => x.error)?.error });
}
