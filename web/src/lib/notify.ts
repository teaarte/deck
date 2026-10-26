import { notifications } from '@mantine/notifications';

// errorText is the message of a failure as the owner reads it.
export const errorText = (e: unknown): string => (e instanceof Error ? e.message : String(e));

// notifyError tells of a failed call, under the title of what failed when there is one.
export function notifyError(e: unknown, title?: string) {
  notifications.show({ title, message: errorText(e), color: 'red', autoClose: 10000 });
}

// notifyNote tells for a moment how a press went.
export function notifyNote(message: string) {
  notifications.show({ message, autoClose: 3000 });
}
