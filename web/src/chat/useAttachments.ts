import { useRef, useState } from 'react';
import { upload } from '../lib/api';

// useAttachments holds the files of a message being written, uploads of a project (the master's by default), kept by id
// once sent up so a retry does not upload them again; a change of the files runs changed, the next send a new message.
export function useAttachments(changed: () => void, project = 'master') {
  const [files, setFiles] = useState<File[]>([]);
  const [dropping, setDropping] = useState(false);
  const sent = useRef<string[] | undefined>(undefined);
  return {
    files,
    dropping,
    setDropping,
    attach: (list: FileList | File[] | null) => {
      if (!list?.length) return;
      // The list of an input is emptied once its value is cleared, before the update runs: it is copied now.
      const got = Array.from(list);
      sent.current = undefined;
      changed();
      setFiles((f) => [...f, ...got]);
    },
    remove: (i: number) => {
      sent.current = undefined;
      changed();
      setFiles(files.filter((_, j) => j !== i));
    },
    ids: async () => {
      if (files.length && !sent.current) sent.current = (await upload(project, files)).map((f) => String(f.id));
      return sent.current;
    },
    clear: () => { setFiles([]); sent.current = undefined; },
  };
}
