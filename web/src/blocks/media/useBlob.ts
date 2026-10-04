import { useEffect, useState } from 'react';
import { fetchFile } from '../../lib/api';

// useBlob reads a file of the item through the API with the token and gives an address of it for the page.
export function useBlob(url: string | undefined, type?: string) {
  const [state, setState] = useState<{ src?: string; blob?: Blob; error?: string }>({});
  useEffect(() => {
    if (!url) return;
    let gone = false;
    let src = '';
    fetchFile(url).then((blob) => {
      src = URL.createObjectURL(type ? new Blob([blob], { type }) : blob);
      if (gone) URL.revokeObjectURL(src);
      else setState({ src, blob });
    }, (e: Error) => { if (!gone) setState({ error: e.message }); });
    return () => { gone = true; if (src) URL.revokeObjectURL(src); };
  }, [url, type]);
  return state;
}
