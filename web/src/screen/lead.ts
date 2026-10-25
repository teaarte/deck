import { createContext, type ReactNode } from 'react';

// ScreenLead is what the frame puts by the title of the screen it shows: beside it, such as the date of Today, and
// under it, such as the state of a module.
export const ScreenLead = createContext<{ beside?: ReactNode; under?: ReactNode }>({});
