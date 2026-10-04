import { useRef, useState } from 'react';
import { upload } from '../lib/api';

// useAttachments holds the files of the message being written; once sent up they keep their ids, so a retry does
// not upload them again. A change of the files makes the next send a new message: changed runs then.
export function useAttachments(changed: () => void) {
  const [files, setFiles] = useState<File[]>([]);
  const [dropping, setDropping] = useState(false);
  const sent = useRef<string[] | undefined>(undefined);
  return {
    files,
    dropping,
    setDropping,
    attach: (list: FileList | File[] | null) => {
      if (!list?.length) return;
      sent.current = undefined;
      changed();
      setFiles((f) => [...f, ...Array.from(list)]);
    },
    remove: (i: number) => {
      sent.current = undefined;
      changed();
      setFiles(files.filter((_, j) => j !== i));
    },
    ids: async () => {
      if (files.length && !sent.current) sent.current = (await upload('master', files)).map((f) => String(f.id));
      return sent.current;
    },
    clear: () => { setFiles([]); sent.current = undefined; },
  };
}
