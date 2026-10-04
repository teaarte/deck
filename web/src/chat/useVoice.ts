import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { call, upload } from '../lib/api';
import { notifyError } from '../lib/notify';
import { record, type Recording } from './voice';

// useVoice records the owner and hands the transcribed text to heard; recording counts the seconds from 1.
export function useVoice(heard: (text: string) => void) {
  const { t } = useTranslation();
  const rec = useRef<Recording | undefined>(undefined);
  const [recording, setRecording] = useState(0);
  const [hearing, setHearing] = useState(false);
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
      notifyError(e, t('chat.mic_failed'));
    }
  };
  const hear = async () => {
    const r = rec.current;
    rec.current = undefined;
    setRecording(0);
    if (!r) return;
    setHearing(true);
    try {
      const blob = await r.stop();
      const [f] = await upload('master', [new File([blob], 'voice.wav', { type: 'audio/wav' })]);
      const out = await call<{ text: string }>('deck.transcribe', { file: String(f.id) });
      heard(out.text);
    } catch (e) {
      notifyError(e, t('chat.voice_failed'));
    } finally {
      setHearing(false);
    }
  };
  return { recording, hearing, listen, hear };
}
