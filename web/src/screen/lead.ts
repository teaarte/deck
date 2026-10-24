import { createContext, type ReactNode } from 'react';

// ScreenLead is what the frame puts under the title of the screen it shows, such as the state of a module.
export const ScreenLead = createContext<ReactNode>(null);
