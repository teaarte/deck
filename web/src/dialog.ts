import { createContext } from 'react';

// DialogClose closes the dialog a block sits in; a form calls it once its call went through.
export const DialogClose = createContext<(() => void) | undefined>(undefined);
