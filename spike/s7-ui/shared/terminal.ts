import { token } from "./api";

// xterm.js is loaded on demand, so it is a separate chunk that only costs
// something when the user opens the terminal tab.
export async function openTerminal(el: HTMLElement): Promise<() => void> {
  const [{ Terminal }, { FitAddon }] = await Promise.all([
    import("@xterm/xterm"),
    import("@xterm/addon-fit"),
    import("@xterm/xterm/css/xterm.css"),
  ]);
  const term = new Terminal({ fontFamily: "ui-monospace, monospace", fontSize: 14, cursorBlink: true });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(el);
  fit.fit();

  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/api/terminal?token=${token()}`);
  ws.binaryType = "arraybuffer";
  const enc = new TextEncoder();
  const sendSize = () => {
    if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
  };
  ws.onopen = () => {
    fit.fit();
    sendSize();
  };
  ws.onmessage = (e) => term.write(new Uint8Array(e.data as ArrayBuffer));
  ws.onclose = () => term.write("\r\n[connection closed]\r\n");
  const d1 = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)));
  const ro = new ResizeObserver(() => {
    fit.fit();
    sendSize();
  });
  ro.observe(el);

  return () => {
    ro.disconnect();
    d1.dispose();
    ws.close();
    term.dispose();
  };
}
