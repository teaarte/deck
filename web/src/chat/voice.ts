// A recording of the microphone as 16 kHz mono WAV, which every speech-to-text model reads.
export interface Recording {
  stop: () => Promise<Blob>;
  cancel: () => void;
}

const RATE = 16000;
export const MAX_SECONDS = 120;

// MicMissing is a page the browser gives no microphone: it does only on https or on the machine itself.
export class MicMissing extends Error {}

// record starts the microphone; the recording stops by itself after MAX_SECONDS and calls onLimit. Call it right
// in the tap: Safari starts audio only from a gesture.
export async function record(onLimit: () => void): Promise<Recording> {
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
  const node = ctx.createScriptProcessor(4096, 1, 1);
  const chunks: Float32Array[] = [];
  let length = 0;
  node.onaudioprocess = (e) => {
    const data = e.inputBuffer.getChannelData(0);
    chunks.push(new Float32Array(data));
    length += data.length;
  };
  source.connect(node);
  node.connect(ctx.destination);
  const timer = setTimeout(onLimit, MAX_SECONDS * 1000);
  const close = () => {
    clearTimeout(timer);
    node.disconnect();
    source.disconnect();
    stream.getTracks().forEach((t) => t.stop());
    void ctx.close();
  };
  return {
    stop: async () => {
      close();
      if (!length) throw new Error('nothing was recorded; check the microphone');
      const all = new Float32Array(length);
      let at = 0;
      for (const c of chunks) { all.set(c, at); at += c.length; }
      return wav(resample(all, ctx.sampleRate, RATE), RATE);
    },
    cancel: close,
  };
}

// fromFile is a recording of any format the browser plays, as the same 16 kHz mono WAV.
export async function fromFile(file: File): Promise<Blob> {
  const ctx = new AudioContext();
  try {
    const audio = await ctx.decodeAudioData(await file.arrayBuffer());
    const mono = new Float32Array(audio.length);
    for (let c = 0; c < audio.numberOfChannels; c++) {
      const data = audio.getChannelData(c);
      for (let i = 0; i < data.length; i++) mono[i] += data[i] / audio.numberOfChannels;
    }
    return wav(resample(mono, audio.sampleRate, RATE), RATE);
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
