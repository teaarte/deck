import { notifications } from '@mantine/notifications';
import i18n from './i18n';

// errorText is the message of a failure as the owner reads it.
export const errorText = (e: unknown): string => (e instanceof Error ? e.message : String(e));

// notifyError tells of a failed call, under the title of what failed when there is one.
export function notifyError(e: unknown, title?: string) {
  notifications.show({ title, message: errorText(e), color: 'red', autoClose: 10000 });
}

// notifyDone tells that a call went through.
export function notifyDone(message: string = i18n.t('done')) {
  notifications.show({ message, color: 'green' });
}
