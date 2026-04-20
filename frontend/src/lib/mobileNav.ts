// Global mobile-drawer open state. Sidebar reads it to apply the
// `rv-open` class; the scrim/hamburger in App toggle it. A single signal
// keeps everything in sync without prop drilling.

import { signal } from '@preact/signals';

export const mobileNavOpen = signal<boolean>(false);

export function openMobileNav() {
  mobileNavOpen.value = true;
}
export function closeMobileNav() {
  mobileNavOpen.value = false;
}
