import { FitAddon } from "@xterm/addon-fit"
import { Terminal } from "@xterm/xterm"
import "@xterm/xterm/css/xterm.css"
import type { TerminalSurface } from "./terminal"

// openXterm puts an xterm into el. The console's route bundle loads it, so
// xterm never reaches the main bundle.
export function openXterm(el: HTMLElement): TerminalSurface {
  const term = new Terminal({ cursorBlink: true, fontFamily: "JetBrains Mono, ui-monospace, monospace", fontSize: 13, scrollback: 5000 })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(el)
  fit.fit()
  return {
    get cols() {
      return term.cols
    },
    get rows() {
      return term.rows
    },
    write: (data) => term.write(data),
    onData: (cb) => term.onData(cb).dispose,
    onResize: (cb) => term.onResize(({ cols, rows }) => cb(cols, rows)).dispose,
    fit: () => fit.fit(),
    focus: () => term.focus(),
    dispose: () => term.dispose(),
  }
}
