import { notifications } from '@mantine/notifications';

// errorText is the message of a failure as the owner reads it.
export const errorText = (e: unknown): string => (e instanceof Error ? e.message : String(e));

// notifyError tells of a failed call, under the title of what failed when there is one.
export function notifyError(e: unknown, title?: string) {
  notifications.show({ title, message: errorText(e), color: 'red', autoClose: 10000 });
}

// shownNotes are the notes on screen by their text, with how many times each was told.
const shownNotes = new Map<string, number>();

// notifyNote tells for a moment how a press went; the same note again counts up in place, not one more on screen.
export function notifyNote(message: string) {
  const id = 'note:' + message;
  const n = (shownNotes.get(message) ?? 0) + 1;
  shownNotes.set(message, n);
  if (n === 1) notifications.show({ id, message, autoClose: 3000, onClose: () => shownNotes.delete(message) });
  else notifications.update({ id, message: `${message} ×${n}`, autoClose: 3000 });
}
