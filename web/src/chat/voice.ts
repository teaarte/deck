const RATE = 16000;
// A piece of a recording is cut at the first pause after PIECE_SECONDS, or at MAX_PIECE_SECONDS without one, so a
// long recording reaches speech to text in pieces it takes.
const PIECE_SECONDS = 45;
const MAX_PIECE_SECONDS = 60;
const BLOCK = 4096;

// MicMissing is a page the browser gives no microphone: it does only on https or on the machine itself.
export class MicMissing extends Error {}

// Pieces cuts samples at rate into pieces of WAV, each handed to piece once cut.
class Pieces {
  private blocks: Float32Array[] = [];
  private length = 0;
  private loud = 0;
  constructor(private rate: number, private piece: (wav: Blob) => void) {}
  push(data: Float32Array) {
    let sum = 0;
    for (const x of data) sum += x * x;
    const rms = Math.sqrt(sum / Math.max(1, data.length));
    this.blocks.push(new Float32Array(data));
    this.length += data.length;
    this.loud += rms;
    const seconds = this.length / this.rate;
    const quiet = rms < 0.25 * (this.loud / this.blocks.length);
    if ((seconds >= PIECE_SECONDS && quiet) || seconds >= MAX_PIECE_SECONDS) this.flush();
  }
  // flush hands what is not cut yet as the last piece; whether anything was.
  flush(): boolean {
    if (!this.length) return false;
    const all = new Float32Array(this.length);
    let at = 0;
    for (const b of this.blocks) { all.set(b, at); at += b.length; }
    this.blocks = [];
    this.length = 0;
    this.loud = 0;
    this.piece(wav(resample(all, this.rate, RATE), RATE));
    return true;
  }
}

// A recording of the microphone as pieces of 16 kHz mono WAV, which every speech-to-text model reads; stop hands
// the last piece and throws when nothing was recorded.
export interface Recording {
  stop: () => void;
  cancel: () => void;
}

// record starts the microphone and hands each piece of the recording to piece while it goes on. Call it right in
// the tap: Safari starts audio only from a gesture.
export async function record(piece: (wav: Blob) => void): Promise<Recording> {
  if (!navigator.mediaDevices?.getUserMedia) throw new MicMissing('the browser gives a microphone only on https');
  const ctx = new AudioContext();
  const resumed = ctx.resume();
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    await resumed;
  } catch (e) {
    void ctx.close();
    throw e;
  }
  const source = ctx.createMediaStreamSource(stream);
  const node = ctx.createScriptProcessor(BLOCK, 1, 1);
  const pieces = new Pieces(ctx.sampleRate, piece);
  let any = false;
  node.onaudioprocess = (e) => {
    pieces.push(e.inputBuffer.getChannelData(0));
    any = true;
  };
  source.connect(node);
  node.connect(ctx.destination);
  const close = () => {
    node.onaudioprocess = null;
    node.disconnect();
    source.disconnect();
    stream.getTracks().forEach((t) => t.stop());
    void ctx.close();
  };
  return {
    stop: () => {
      close();
      pieces.flush();
      if (!any) throw new Error('nothing was recorded; check the microphone');
    },
    cancel: close,
  };
}

// fromFile is a recording of any format the browser plays, as the same pieces of 16 kHz mono WAV.
export async function fromFile(file: File): Promise<Blob[]> {
  const ctx = new AudioContext();
  try {
    const audio = await ctx.decodeAudioData(await file.arrayBuffer());
    const mono = new Float32Array(audio.length);
    for (let c = 0; c < audio.numberOfChannels; c++) {
      const data = audio.getChannelData(c);
      for (let i = 0; i < data.length; i++) mono[i] += data[i] / audio.numberOfChannels;
    }
    const out: Blob[] = [];
    const pieces = new Pieces(audio.sampleRate, (w) => out.push(w));
    for (let at = 0; at < mono.length; at += BLOCK) pieces.push(mono.subarray(at, at + BLOCK));
    pieces.flush();
    return out;
  } finally {
    void ctx.close();
  }
}

function resample(data: Float32Array, from: number, to: number): Float32Array {
  if (from === to) return data;
  const out = new Float32Array(Math.floor((data.length * to) / from));
  const step = from / to;
  for (let i = 0; i < out.length; i++) {
    const start = Math.floor(i * step), end = Math.min(data.length, Math.floor((i + 1) * step));
    let sum = 0;
    for (let j = start; j < end; j++) sum += data[j];
    out[i] = end > start ? sum / (end - start) : data[start] ?? 0;
  }
  return out;
}

function wav(data: Float32Array, rate: number): Blob {
  const buf = new ArrayBuffer(44 + data.length * 2);
  const v = new DataView(buf);
  const text = (at: number, s: string) => [...s].forEach((c, i) => v.setUint8(at + i, c.charCodeAt(0)));
  text(0, 'RIFF'); v.setUint32(4, 36 + data.length * 2, true); text(8, 'WAVE');
  text(12, 'fmt '); v.setUint32(16, 16, true); v.setUint16(20, 1, true); v.setUint16(22, 1, true);
  v.setUint32(24, rate, true); v.setUint32(28, rate * 2, true); v.setUint16(32, 2, true); v.setUint16(34, 16, true);
  text(36, 'data'); v.setUint32(40, data.length * 2, true);
  for (let i = 0; i < data.length; i++) {
    const s = Math.max(-1, Math.min(1, data[i]));
    v.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
  }
  return new Blob([buf], { type: 'audio/wav' });
}
