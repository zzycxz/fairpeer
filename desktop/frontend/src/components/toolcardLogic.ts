// Calculate initial open state
export function getInitialOpenState(itemStatus: string, hasNested: boolean, isShell: boolean, hasOutput = false): boolean {
  if (hasNested || isShell) {
    // Shell cards with output stay open after completion so the user sees
    // the result without clicking — a closed card showing nothing after a
    // long build reads as "something went wrong". Empty shells still close.
    if (isShell) {
      return itemStatus === "running" || hasOutput;
    }
    return itemStatus === "running";
  }
  return false;
}

// Keep-mounted logic
export function shouldKeepMounted(hasBeenOpened: boolean, currentOpen: boolean): boolean {
  return hasBeenOpened || currentOpen;
}
