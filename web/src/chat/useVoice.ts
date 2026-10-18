import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { call } from '../lib/api';
import { notifyError } from '../lib/notify';
import { MicMissing } from './voice';
import * as voice from './voiceStore';

// NoMic is why the microphone is not there, with the https address of deck on the tailnet when it has one.
export interface NoMic { https?: string }

// useVoice records the owner, or takes a recording as a file, and hands the transcribed text to heard; the
// recording and its text outlive the composer, and one that failed stays to be turned into text again.
export function useVoice(heard: (text: string) => void) {
  const { t } = useTranslation();
  const state = voice.useVoiceState();
  const [noMic, setNoMic] = useState<NoMic>();
  const to = useRef(heard);
  to.current = heard;
  useEffect(() => voice.hearBy((text) => to.current(text)), []);
  useEffect(() => {
    if (state.failed) notifyError(new Error(state.failed), t('chat.voice_failed'));
  }, [state.failed]);
  const listen = async () => {
    try {
      await voice.listen();
    } catch (e) {
      if (!(e instanceof MicMissing)) {
        notifyError(e, t('chat.mic_failed'));
        return;
      }
      setNoMic({});
      const at = await call<{ https?: string }>('deck.secure_address', {}).catch(() => ({ https: undefined }));
      setNoMic({ https: at.https });
    }
  };
  const hear = () => {
    try {
      voice.stop();
    } catch (e) {
      notifyError(e, t('chat.voice_failed'));
    }
  };
  const take = async (file: File) => {
    setNoMic(undefined);
    await voice.take(file).catch((e) => notifyError(e, t('chat.voice_failed')));
  };
  return {
    recording: state.seconds, hearing: state.hearing, failed: state.failed, listen, hear, noMic, take,
    again: () => void voice.again(), discard: voice.discard, closeNoMic: () => setNoMic(undefined),
  };
}
