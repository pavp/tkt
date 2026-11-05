/** What each machine of the core keeps in one window: its tab, selection and board, so switching machines and back
 *  finds each as it was. Pure, for `node --test`. */

export function machineKey(on) {
  return on ?? "local";
}

/** The part of the window's state that belongs to the machine it shows. */
export function stashed(s) {
  return { tab: s.tab, selected: s.selected, inStrip: false, stripSel: 0, answers: s.answers, cachedAt: s.cachedAt };
}

/** The state to show machine on: what it had in this window, else its kept copy (cached, from unpackCache), else
 *  a fresh Sprint. */
export function restored(states, on, cached) {
  const kept = states[machineKey(on)];
  if (kept) return { ...kept };
  if (cached) return { tab: cached.tab ?? "sprint", selected: null, inStrip: false, stripSel: 0, answers: cached.answers, cachedAt: cached.at };
  return { tab: "sprint", selected: null, inStrip: false, stripSel: 0, answers: {}, cachedAt: null };
}
