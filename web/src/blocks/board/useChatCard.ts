import { useEffect } from 'react';
import { setChatCard } from '../../chat/chatStore';

// useChatCard tells the chat which card is open, so a message about "this" goes with it.
export function useChatCard(id: number) {
  useEffect(() => { setChatCard(id); return () => setChatCard(undefined); }, [id]);
}
