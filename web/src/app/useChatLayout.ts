import { useLocalStorage } from '@mantine/hooks';
import { useChatPanel } from '../chat/chatStore';

// useChatLayout is the layout this device keeps: the chat open, full or its width.
export function useChatLayout() {
  const { open: chat, setOpen: setChat } = useChatPanel();
  const [full, setFull] = useLocalStorage({ key: 'deck-chat-full', defaultValue: false });
  const [width, setWidth] = useLocalStorage({ key: 'deck-chat-width', defaultValue: 420 });
  return { chat, setChat, full, setFull, width, setWidth };
}
