import { events } from "./events";

// How the camera node reaches the service, and what it does when it cannot.
// Mountain villages lose signal first, so the node is built to need less:
//
//   4G         the whole picture, every 5 s; SAM 3.1 measures it on the service.
//   2G         only the gauge and what is around it, a few kB; SAM 3.1 still measures it.
//   No signal  the node reads the gauge from its own pixels, sounds its local
//              siren at the evacuate line, and keeps each reading as a 10-byte
//              packet; when the signal is back it sends them all.
//   LoRa       the same 10-byte packets over the radio, one every 30 s.
//
// The node learns to read the gauge by itself while it has signal: each time
// SAM 3.1 says where the water is, the rows of the gauge above it are what
// "dry" looks like. Without signal, the waterline is where the rows stop
// looking dry, counted from the bottom of the gauge up.

export type Mode = "4g" | "2g" | "off" | "lora";
export type Calib = { px_per_unit: number; zero_y: number; unit: string; col_x1: number; col_x2: number; col_y?: number; slope?: number; top?: number };

// colsAt is where the gauge columns are at frame row y: a gauge seen from the
// bank leans in the picture, so they shift `slope` px per row.
export function colsAt(c: Calib, y: number) {
  const d = (c.slope ?? 0) * (y - (c.col_y ?? 0));
  return { x1: c.col_x1 + d, x2: c.col_x2 + d };
}
export type Local = { levelCm?: number; y?: number; confidence: number };

const UNIT_CM: Record<string, number> = { CM: 1, M: 100, FT: 30.48 };
export const BPS_2G = 32_000; // a 2G (GPRS/EDGE) uplink in a valley, bits per second
export const LORA_EVERY_MS = 30_000; // one packet in 30 s keeps a LoRa node within its duty cycle
export const PACKET = 10;

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

// --- reading the gauge on the node --------------------------------------------------
// Each row across the gauge columns: mean R, G, B and how much the brightness
// varies along it (the printed marks: a dry gauge is striped, water is even).
type Band = { y0: number; rows: Float32Array };
const F = 4; // values per row

export function bandOf(c: Calib, h: number) {
  const top = c.top != null ? c.zero_y - c.top * c.px_per_unit : c.zero_y - 600;
  const y0 = Math.max(0, Math.round(top) + 4), y1 = Math.min(h - 1, Math.round(c.zero_y) + 40);
  const a = colsAt(c, y0), b = colsAt(c, y1);
  return { x0: Math.max(0, Math.round(Math.min(a.x1, b.x1))), x1: Math.round(Math.max(a.x2, b.x2)), y0, y1 };
}

// How far a row's colour may be from how it looked dry and still count as dry.
// Dry rows match their learned look within a few units (after the light fit);
// a gauge far from the camera is a few pixels wide and averages to a grey that
// shadowed water comes close to, so the margin stays tight.
const WET = 15;

export class LocalGauge {
  private dry = new Map<number, number[]>(); // row → what it looks like above the water
  private last: { y: number; t: number } | null = null; // the last waterline let through
  private held: number | null = null; // a far-off waterline waiting for the next frame to confirm it
  learned = 0; // frames learned from

  // capture averages each frame row of the gauge, from a close-up of the band
  // rendered k times finer than the frame (see CameraNode.closeUp).
  capture(closeUp: (r: { x: number; y: number; w: number; h: number }, k: number) => ImageData, frameH: number, c: Calib | undefined): Band | null {
    if (!c) return null;
    const b = bandOf(c, frameH);
    if (b.x1 <= b.x0 || b.y1 <= b.y0) return null;
    const w = b.x1 - b.x0 + 1, h = b.y1 - b.y0 + 1;
    // A gauge a few pixels wide in the frame is a grey smear, as murky water
    // is: the node reads it with about 100 px per metre.
    const k = Math.max(1, Math.min(4, Math.ceil(100 / (c.px_per_unit * (100 / (UNIT_CM[c.unit] ?? 100))))));
    const img = closeUp({ x: b.x0, y: b.y0, w, h }, k);
    const px = img.data, W = img.width;
    const rows = new Float32Array(h * F);
    for (let y = 0; y < h; y++) {
      // Only the gauge's own columns on this row: the band is wider when it leans.
      const cs = colsAt(c, b.y0 + y);
      const from = Math.max(0, Math.round((cs.x1 - b.x0) * k)), to = Math.min(W - 1, Math.round((cs.x2 - b.x0) * k));
      let r = 0, g = 0, bl = 0, l2 = 0, n = 0;
      for (let sy = y * k; sy < Math.min(img.height, (y + 1) * k); sy++) {
        for (let x = from; x <= to; x++) {
          const i = (sy * W + x) * 4;
          r += px[i];
          g += px[i + 1];
          bl += px[i + 2];
          const l = (px[i] + px[i + 1] + px[i + 2]) / 3;
          l2 += l * l;
          n++;
        }
      }
      n = Math.max(1, n);
      const lm = (r + g + bl) / 3 / n;
      rows.set([r / n, g / n, bl / n, Math.sqrt(Math.max(0, l2 / n - lm * lm))], y * F);
    }
    return { y0: b.y0, rows };
  }

  // learn takes the rows above the waterline SAM 3.1 found as dry gauge.
  learn(band: Band, waterlineY: number) {
    const n = band.rows.length / F;
    for (let i = 0; i < n; i++) {
      const y = band.y0 + i;
      if (y > waterlineY - 4) break;
      const v = Array.from(band.rows.subarray(i * F, i * F + F));
      const old = this.dry.get(y);
      this.dry.set(y, old ? old.map((o, k) => o * 0.6 + v[k] * 0.4) : v);
    }
    this.learned++;
  }

  // read finds the waterline: the top of the wet stretch that reaches the
  // bottom of the gauge. Searching from below, a log floating nearer the camera
  // (it covers the gauge above the water in the picture) is a wet patch with
  // dry gauge under it, not the water. Comparing a row with its neighbours too
  // tolerates a camera that sways in the wind.
  read(band: Band, c: Calib): Local {
    if (!this.learned) return { confidence: 0 };
    const n = band.rows.length / F;
    // The light changes (a cloud, the sun, the water itself lighting the
    // gauge): fit, per colour, how the top of the gauge (above any flood worth
    // a siren) looks now against how it looked dry, and compare every row
    // through that fit.
    const top = Math.max(6, Math.min(30, Math.round(0.3 * c.px_per_unit * (100 / (UNIT_CM[c.unit] ?? 100))))); // rows in its top 30 cm
    const gain = [0, 1, 2].map((ch) => {
      const xs: number[] = [], ys: number[] = [];
      for (let i = 0; i < Math.min(top, n); i++) {
        const ref = this.dry.get(band.y0 + i);
        if (ref) {
          xs.push(ref[ch]);
          ys.push(band.rows[i * F + ch]);
        }
      }
      const m = xs.length, mx = xs.reduce((a, b) => a + b, 0) / (m || 1), my = ys.reduce((a, b) => a + b, 0) / (m || 1);
      let sxy = 0, sxx = 0;
      for (let k = 0; k < m; k++) {
        sxy += (xs[k] - mx) * (ys[k] - my);
        sxx += (xs[k] - mx) ** 2;
      }
      const a = sxx > 50 ? Math.max(0.5, Math.min(2.5, sxy / sxx)) : 1;
      return { a, b: my - a * mx };
    });
    const wet: boolean[] = [];
    let known = 0, matched = 0;
    // A row is matched against its neighbours too, for a camera swaying a few
    // centimetres on its pole: rows, as many as 3.5 cm of gauge make.
    const sway = Math.max(1, Math.min(3, Math.round(0.035 * c.px_per_unit * (100 / (UNIT_CM[c.unit] ?? 100)))));
    for (let i = 0; i < n; i++) {
      const y = band.y0 + i;
      let best = Infinity;
      for (let d = -sway; d <= sway; d++) {
        const ref = this.dry.get(y + d);
        if (!ref) continue;
        const e = [0, 1, 2].map((ch) => band.rows[i * F + ch] - (gain[ch].a * ref[ch] + gain[ch].b));
        const stripes = band.rows[i * F + 3] - ref[3] * (gain[0].a + gain[1].a + gain[2].a) / 3; // the marks, as bright as the light now
        best = Math.min(best, Math.hypot(e[0], e[1], e[2], stripes));
      }
      wet.push(best > WET); // never seen dry counts as wet: below the lowest water it learned
      if (best !== Infinity) {
        known++;
        if (best <= WET) matched++;
      }
    }
    // Up from the bottom while the rows stay wet; a few dry-looking rows (foam,
    // a reflection) do not end the stretch.
    const GAP = 4;
    let at = n, dryRun = 0;
    for (let i = n - 1; i >= 0; i--) {
      if (wet[i]) {
        at = i;
        dryRun = 0;
      } else if (++dryRun > GAP) {
        break;
      }
    }
    // Too few dry rows above the line: the picture changed as a whole (night,
    // fog, a covered lens), not the water. A blind node never claims a level.
    const dryAbove = wet.slice(0, at).filter((w) => !w).length;
    if (at >= n || dryAbove < 8) return { confidence: 0 };
    // A waterline far from the last one (a log under the water that looks like
    // dry gauge) waits for the next frame: if that one agrees, it goes through.
    // A median would lag a flash flood by a reading; this lags only a jump.
    let y = band.y0 + at;
    const far = Math.abs(y - (this.last?.y ?? y)) > 0.8 * c.px_per_unit * (100 / (UNIT_CM[c.unit] ?? 100));
    if (this.last && Date.now() - this.last.t < 15_000 && far) {
      const confirmed = this.held != null && Math.abs(this.held - y) < 0.3 * c.px_per_unit * (100 / (UNIT_CM[c.unit] ?? 100));
      if (!confirmed) {
        this.held = y;
        y = this.last.y;
      }
    }
    if (y !== this.last?.y || !far) this.held = null;
    this.last = { y, t: Date.now() };
    const confidence = Math.min(1, known ? matched / known + 0.3 : 0);
    return { y, levelCm: ((c.zero_y - y) / c.px_per_unit) * (UNIT_CM[c.unit] ?? 100), confidence };
  }
}

// --- 10-byte packets --------------------------------------------------------------------
// Big-endian: unix seconds (4) · level in mm (2, 0xFFFF = could not see) ·
// confidence × 255 (1) · flags (1, bit 0: local siren on) · sequence (2).
let seq = 0;
export function packet(t: number, r: Local, siren: boolean): Uint8Array {
  const b = new Uint8Array(PACKET);
  const v = new DataView(b.buffer);
  v.setUint32(0, Math.floor(t / 1000));
  v.setUint16(4, r.levelCm == null ? 0xffff : Math.max(0, Math.min(0xfffe, Math.round(r.levelCm * 10))));
  v.setUint8(6, Math.round(Math.max(0, Math.min(1, r.confidence)) * 255));
  v.setUint8(7, siren ? 1 : 0);
  v.setUint16(8, seq++ & 0xffff);
  return b;
}

// --- the link, as the visitor sees it ------------------------------------------------------
type Rules = { level: number; siren: boolean }[];

class Link {
  mode: Mode = "4g";
  queue: Uint8Array[] = []; // readings kept while there was no signal
  sentToday = 0;
  lastBytes = 0;
  lastLora = 0;
  sirenOn = false;
  private above = 0;
  private lines: Rules = [];

  constructor() {
    document.querySelectorAll<HTMLButtonElement>("[data-net]").forEach((b) =>
      b.addEventListener("click", () => this.set(b.dataset.net as Mode)),
    );
    this.show();
  }

  set(m: Mode) {
    this.mode = m;
    document.querySelectorAll("[data-net]").forEach((b) => b.setAttribute("aria-checked", String((b as HTMLElement).dataset.net === m)));
    document.body.dataset.net = m;
    if (m === "4g" || m === "2g") this.siren(false);
    this.show();
    dispatchEvent(new Event("net-change"));
  }

  get online() {
    return this.mode === "4g" || this.mode === "2g";
  }

  // The alert lines the node keeps for itself, from the station's settings.
  async loadLines(station: string) {
    const v = await fetch(`/api/stations/${station}/alerts`).then((r) => r.json()).catch(() => null);
    const rules = v?.config?.rules;
    if (!rules) return;
    this.lines = (["attention", "alert", "evacuate"] as const)
      .map((k) => rules[k])
      .filter((r) => r?.enabled && r.level_cm != null)
      .map((r) => ({ level: r.level_cm as number, siren: (r.channels ?? []).includes("siren") }));
  }

  sent(bytes: number) {
    this.lastBytes = bytes;
    this.sentToday += bytes;
    this.show();
  }

  // local applies the node's own reading: two in a row at or above a siren line
  // sound the siren (one odd frame never does), and it stays on until the water
  // is back below the line.
  local(r: Local) {
    const line = this.lines.filter((l) => l.siren).map((l) => l.level).sort((a, b) => a - b)[0];
    if (line == null || r.levelCm == null) return;
    this.above = r.levelCm >= line ? this.above + 1 : 0;
    if (this.above >= 2 && !this.sirenOn) this.siren(true);
    if (r.levelCm < line - 20 && this.sirenOn) this.siren(false);
  }

  private audio: AudioContext | null = null;
  private timer = 0;
  siren(on: boolean) {
    this.sirenOn = on;
    $("siren").hidden = !on;
    document.body.classList.toggle("siren-on", on);
    clearInterval(this.timer);
    if (!on) return;
    // A rising and falling tone, like the horn on a pole, while it is on.
    const wail = () => {
      try {
        this.audio ??= new AudioContext();
        const ctx = this.audio, t = ctx.currentTime;
        const osc = ctx.createOscillator(), gain = ctx.createGain();
        osc.type = "sawtooth";
        gain.gain.value = 0.05;
        osc.frequency.setValueAtTime(520, t);
        osc.frequency.linearRampToValueAtTime(980, t + 1.2);
        osc.frequency.linearRampToValueAtTime(520, t + 2.4);
        osc.connect(gain).connect(ctx.destination);
        osc.start(t);
        osc.stop(t + 2.4);
      } catch {
        /* no audio */
      }
    };
    wail();
    this.timer = window.setInterval(() => (this.sirenOn ? wail() : clearInterval(this.timer)), 2600);
  }

  show() {
    const kb = (b: number) => (b >= 1024 ? `${(b / 1024).toFixed(b >= 10240 ? 0 : 1)} KB` : `${b} bytes`);
    const per = this.lastBytes ? kb(this.lastBytes) : "–";
    $("net-line").textContent = {
      "4g": `Whole picture to SAM 3.1 · ${per} per reading`,
      "2g": `The gauge and the stream by it to SAM 3.1 · ${per} per reading`,
      off: "No signal: the node reads the gauge itself",
      lora: `Read on the node · ${PACKET} bytes by radio every 30 s`,
    }[this.mode];
    const q = this.queue.length;
    $("net-queue").hidden = !q;
    $("net-queue").textContent = `${q} reading${q === 1 ? "" : "s"} kept on the node (${q * PACKET} bytes), sent when the signal is back`;
    $("net-total").textContent = `Sent this session: ${kb(this.sentToday)}`;
  }
}

export const link = new Link();
// The lines can change in the settings: keep the node's copy in step.
events.addEventListener("alerts_config", () => link.loadLines($("station-id").textContent || ""));
