import { createContext, type ReactNode } from 'react';

// ScreenLead is what the frame puts beside a screen's title (Today's date) and under it (a module's state); place is
// the title of the frame's tab, not repeated; slot is the row of tabs, where a phone shows the side panel's button.
export const ScreenLead = createContext<{ beside?: ReactNode; under?: ReactNode; place?: string; slot?: HTMLElement | null }>({});
