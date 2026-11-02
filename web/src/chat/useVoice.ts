import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLiveRead } from '../hooks/useLiveRead';
import type { Action } from '../lib/catalog.gen';
import { notifyError } from '../lib/notify';
import { MicMissing } from './voice';
import * as voice from './voiceStore';

// NoMic is why the microphone is not there, with the https address of deck on the tailnet when it has one.
export interface NoMic { https?: string }

// useVoice records the owner, or takes a recording as a file, and hands the transcribed text to heard; the
// recording and its text outlive the composer, and one that failed stays to be turned into text again.
export function useVoice(heard: (text: string) => void, transcribe?: Action) {
  const { t } = useTranslation();
  const state = voice.useVoiceState();
  const [missing, setMissing] = useState(false);
  const at = useLiveRead<{ https?: string }>('deck.secure_address_get', {}, {}, !missing);
  const noMic: NoMic | undefined = missing ? { https: at.https } : undefined;
  const to = useRef(heard);
  to.current = heard;
  useEffect(() => voice.hearBy((text) => to.current(text), transcribe), [transcribe?.command]);
  useEffect(() => {
    if (state.failed) notifyError(new Error(state.failed), t('The recording was not turned into text'));
  }, [state.failed]);
  const listen = async () => {
    try {
      await voice.listen();
    } catch (e) {
      if (!(e instanceof MicMissing)) {
        notifyError(e, t('The microphone did not start'));
        return;
      }
      setMissing(true);
    }
  };
  const hear = () => {
    try {
      voice.stop();
    } catch (e) {
      notifyError(e, t('The recording was not turned into text'));
    }
  };
  const take = async (file: File) => {
    setMissing(false);
    await voice.take(file).catch((e) => notifyError(e, t('The recording was not turned into text')));
  };
  return {
    recording: state.seconds, hearing: state.hearing, failed: state.failed, listen, hear, noMic, take,
    again: () => void voice.again(), discard: voice.discard, closeNoMic: () => setMissing(false),
  };
}
