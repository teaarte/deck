import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { call, upload } from '../lib/api';
import { notifyError } from '../lib/notify';
import { fromFile, MicMissing, record, type Recording } from './voice';

// NoMic is why the microphone is not there, with the https address of deck on the tailnet when it has one.
export interface NoMic { https?: string }

// useVoice records the owner, or takes a recording as a file, and hands the transcribed text to heard; recording
// counts the seconds from 1.
export function useVoice(heard: (text: string) => void) {
  const { t } = useTranslation();
  const rec = useRef<Recording | undefined>(undefined);
  const [recording, setRecording] = useState(0);
  const [hearing, setHearing] = useState(false);
  const [noMic, setNoMic] = useState<NoMic>();
  useEffect(() => {
    if (!recording) return;
    const timer = setInterval(() => setRecording((n) => n + 1), 1000);
    return () => clearInterval(timer);
  }, [recording > 0]);
  useEffect(() => () => rec.current?.cancel(), []);
  const listen = async () => {
    try {
      rec.current = await record(() => void hear());
      setRecording(1);
    } catch (e) {
      if (!(e instanceof MicMissing)) {
        notifyError(e, t('chat.mic_failed'));
        return;
      }
      setNoMic({});
      const rows = await call<{ subject: string; status: string; error?: string }[]>('deck.health_list', { machine: true }).catch(() => []);
      const net = rows.find((r) => r.subject === 'tailscale' && r.status === 'ok' && r.error?.startsWith('https://'));
      setNoMic({ https: net?.error });
    }
  };
  const transcribe = async (wav: () => Promise<Blob>) => {
    setHearing(true);
    try {
      const [f] = await upload('master', [new File([await wav()], 'voice.wav', { type: 'audio/wav' })]);
      const out = await call<{ text: string }>('deck.transcribe', { file: String(f.id) });
      heard(out.text);
    } catch (e) {
      notifyError(e, t('chat.voice_failed'));
    } finally {
      setHearing(false);
    }
  };
  const hear = async () => {
    const r = rec.current;
    rec.current = undefined;
    setRecording(0);
    if (r) await transcribe(() => r.stop());
  };
  const take = async (file: File) => {
    setNoMic(undefined);
    await transcribe(() => fromFile(file));
  };
  return { recording, hearing, listen, hear, noMic, take, closeNoMic: () => setNoMic(undefined) };
}
