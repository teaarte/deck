import { useEffect, useState } from 'react';
import { fetchFile } from '../../lib/api';
import { key } from '../../lib/signin';

// useBlob reads a file of the item through the API and gives an address of it for the page.
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

// useSrc is the address a tag of the page, such as a player, reads a file at: the file's own by the cookie of the
// browser, else a copy read with the key of the page, which a tag cannot send.
export function useSrc(url: string | undefined, type?: string): string | undefined {
  const keyed = !!key();
  const { src } = useBlob(keyed ? url : undefined, type);
  return keyed ? src : url;
}
