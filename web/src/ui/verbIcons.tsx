import { IconArchive, IconArrowBackUp, IconArrowUpRight, IconCheck, IconCpu, IconDownload, IconFlask, IconKey, IconLogin, IconMessage,
  IconPencil, IconPlayerPause, IconPlayerPlay, IconPlayerStop, IconRefresh, IconReplace, IconSend, IconSettings, IconTrash, IconX, type Icon } from '@tabler/icons-react';
import type { Action } from '../lib/catalog.gen';
import { NamedIcon } from './NamedIcon';

// verbIcons are the icons of deck's own verbs; a button with a label and no icon of its own has none.
const verbIcons: Record<string, Icon> = {
  open: IconArrowUpRight, stop: IconPlayerStop, cancel_run: IconPlayerStop, run_now: IconPlayerPlay, resume: IconPlayerPlay,
  continue: IconPlayerPlay, pause: IconPlayerPause, retry: IconRefresh, remove: IconTrash, delete: IconTrash, archive: IconArchive,
  download: IconDownload, allow: IconCheck, yes: IconCheck, mark_read: IconCheck, mark_done: IconCheck, deny: IconX,
  edit: IconPencil, replace: IconReplace, provider: IconCpu, try: IconFlask, set: IconKey,
  answer: IconMessage, tell: IconMessage, send: IconSend, undo: IconArrowBackUp, settings: IconSettings, sign_in: IconLogin,
};

export const verbIcon = (act: Action) => {
  if (act.icon) return <NamedIcon name={act.icon} fallback={act.label || act.verb || ''} size={14} stroke={2} />;
  const I = !act.label && act.verb ? verbIcons[act.verb] : undefined;
  return I ? <I size={14} stroke={2} /> : undefined;
};
