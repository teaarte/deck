import { useMediaQuery } from '@mantine/hooks';

const PHONE = '(max-width: 48em)';

// usePhone says whether the screen is as narrow as a phone's.
export const usePhone = () => useMediaQuery(PHONE, window.matchMedia(PHONE).matches, { getInitialValueInEffect: false });
