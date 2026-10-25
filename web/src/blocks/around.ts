import { createContext } from 'react';

// Around is the title of the tab or the dialog a block is in, which the title of the block does not say again.
export const Around = createContext<string | undefined>(undefined);
