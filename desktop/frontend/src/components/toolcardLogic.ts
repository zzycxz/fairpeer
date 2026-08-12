// Calculate initial open state
export function getInitialOpenState(itemStatus: string, hasNested: boolean, isShell: boolean): boolean {
  if (hasNested || isShell) {
    return itemStatus === "running";
  }
  return false;
}

// Keep-mounted logic
export function shouldKeepMounted(hasBeenOpened: boolean, currentOpen: boolean): boolean {
  return hasBeenOpened || currentOpen;
}
