import { createContext, type ReactNode } from 'react';

// ScreenLead is what the frame puts beside the title of a screen, such as the date of Today, and under it, such as
// the state of a module; place is the title of the frame's tab of the screen, which the screen does not repeat.
export const ScreenLead = createContext<{ beside?: ReactNode; under?: ReactNode; place?: string }>({});
