import "./sam-view.css";
import { events } from "./events";
import { STATION } from "./station";
import { GAUGE_HEIGHT } from "./world";
import { FRAME_W, FRAME_H } from "./camera-node";

// The "SAM view": the main view becomes the camera node's picture, with what
// SAM 3.1 finds on it drawn on top, live, like layers on a map: the water, the
// people and logs, and the gauge with the waterline. With a GPU linked to the
// service, open-weights SAM 3.1 looks at the picture a few times a second; without
// one, the view shows the mask of the last frame measured through the Meta Model
// API (every 5 s while the camera node runs). Either way the level itself comes
// from that measurement, never from this view.
//
// The same view works on the real river (El Reno): its last photo, with the mask.

type Calibration = { px_per_unit: number; zero_y: number; unit: string; col_x1: number; col_x2: number; top?: number };
type Reading = { t: string; level_cm?: number; waterline_y?: number; coverage: number; confidence: number; sam_ms: number; blind?: boolean };
type Summary = { id: string; calibration?: Calibration; last?: Reading; rating?: object };
type Measured = { view: { t: string; prompt: string; objects: number }; frame: string; mask: string; station: Summary };
type Obj = { label: string; box: [number, number, number, number]; score: number; area: number; mask?: string };
type Live = { result: { width: number; height: number; ms: number; objects: Obj[] }; round_trip_ms: number };

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const view = $("sam-view");
const toggle = $<HTMLButtonElement>("sam-toggle");
const stage = $("sam-stage");
const video = $<HTMLCanvasElement>("sam-video");
const photo = $<HTMLImageElement>("sam-photo");
const water = $<HTMLCanvasElement>("sam-water");
const things = $<HTMLCanvasElement>("sam-things");
const tags = $<HTMLCanvasElement>("sam-tags"); // boxes and labels, sharper than the masks
const lines = $<HTMLCanvasElement>("sam-lines");
video.width = FRAME_W;
video.height = FRAME_H;
const videoCtx = video.getContext("2d")!;

const UNIT_CM: Record<string, number> = { CM: 1, M: 100, FT: 30.48 };
// What to look for. "water" finds the twin's stream and the real river alike;
// on real rivers "mud" adds a debris flow, which SAM does not call water (on the
// twin it finds the logs instead, so it is not asked there).
const WATER = ["water", "mud"];
const PROMPTS: Record<string, string[]> = { twin: ["water", "person", "log"], river: [...WATER, "person", "vehicle", "log"] };
const FRESH_MS = 60_000; // a measured level older than this is not drawn as the current one
const COLORS: Record<string, [number, number, number]> = { person: [255, 72, 140], log: [255, 176, 32], vehicle: [120, 255, 110] };
const NAMES: Record<string, [string, string]> = { person: ["person", "people"], log: ["log", "logs"], vehicle: ["vehicle", "vehicles"] };
const WATER_RGB: [number, number, number] = [24, 96, 255];

let active = false;
let source: "twin" | "river" = "twin";
const id = () => (source === "twin" ? STATION : "el-reno");
let gpu = "off";
let station: Summary | null = null;
let liveY: number | null = null; // waterline the live mask shows, in frame pixels
let pumping = false;
let shown = 0; // results drawn in the last seconds, for the frame rate
const stamps: number[] = [];

// --- open and close -----------------------------------------------------------
function setActive(on: boolean) {
  active = on;
  view.hidden = !on;
  toggle.setAttribute("aria-pressed", String(on));
  document.body.classList.toggle("sam-on", on);
  if (!on) return;
  load();
  checkGPU();
}
toggle.addEventListener("click", () => setActive(!active));
$("sam-close").addEventListener("click", () => setActive(false));
addEventListener("keydown", (e) => e.key === "Escape" && active && setActive(false));

view.querySelectorAll<HTMLButtonElement>("[data-sam-source]").forEach((b) =>
  b.addEventListener("click", () => {
    source = b.dataset.samSource as typeof source;
    view.querySelectorAll("[data-sam-source]").forEach((x) => x.setAttribute("aria-selected", String(x === b)));
    stage.classList.toggle("river", source === "river");
    clear();
    load();
    if (source === "twin" && gpu === "ready") pump();
  }),
);
view.querySelectorAll<HTMLInputElement>("[data-layer]").forEach((c) =>
  c.addEventListener("change", () => stage.classList.toggle(`no-${c.dataset.layer}`, !c.checked)),
);

// --- the picture ----------------------------------------------------------------
// The twin: main.ts hands over the node's frame as it renders it.
export const samView = {
  get open() {
    return active;
  },
  get twin() {
    return active && source === "twin";
  },
  frame(src: HTMLCanvasElement) {
    videoCtx.drawImage(src, 0, 0);
  },
};

// --- the measured frame (Meta Model API) ---------------------------------------------
async function load() {
  const want = source;
  const [res, sum] = await Promise.all([
    fetch(`/api/stations/${id()}/sam`).catch(() => null),
    fetch(`/api/stations/${id()}`).then((r) => (r.ok ? (r.json() as Promise<Summary>) : null)).catch(() => null),
  ]);
  if (want !== source) return;
  station = sum;
  const m = res?.ok ? ((await res.json()) as Measured) : null;
  if (m) station = { ...m.station, id: id() };
  if (source === "river") {
    if (!m) return status("Waiting for the next photo from the USGS camera (one an hour).");
    photo.src = m.frame;
    await photo.decode().catch(() => {});
    stage.style.setProperty("--ar", `${photo.naturalWidth} / ${photo.naturalHeight}`);
    if (gpu === "ready") return segmentPhoto(m.frame);
  }
  // No GPU (or the river without one): the mask SAM drew on the measured frame.
  if (gpu !== "ready" && m) await paintMeasured(m.mask);
  drawLines();
  stats();
}

async function paintMeasured(maskURL: string) {
  const bits = await maskBits(maskURL);
  if (!bits) return;
  paintWater([bits], bits.w, bits.h);
  for (const c of [things, tags]) c.getContext("2d")!.clearRect(0, 0, c.width, c.height);
  found = [];
  liveY = null;
}

events.addEventListener("reading", (e) => {
  if (!active) return;
  const s = JSON.parse((e as MessageEvent).data) as Summary;
  if (s.id === id()) load();
});

// --- the GPU, live --------------------------------------------------------------------
let gpuTimer = 0;
async function checkGPU() {
  clearTimeout(gpuTimer);
  if (!active) return;
  const h = await fetch("/api/gpu").then((r) => r.json()).catch(() => ({ stage: "off" }));
  const was = gpu;
  gpu = h.stage;
  stats();
  if (gpu === "ready") {
    if (source === "twin") pump();
    else if (was !== "ready") load();
  }
  gpuTimer = window.setTimeout(checkGPU, gpu === "ready" ? 30_000 : 15_000);
}

// pump sends the twin's picture to the GPU, one frame at a time: the next
// leaves when the last answer arrives, so a slow link lowers the frame rate
// instead of piling up frames.
async function pump() {
  if (pumping) return;
  pumping = true;
  const small = document.createElement("canvas");
  small.width = 640;
  small.height = 360;
  const g = small.getContext("2d")!;
  try {
    while (active && source === "twin" && gpu === "ready") {
      if (document.hidden) {
        await sleep(1000);
        continue;
      }
      g.drawImage(video, 0, 0, small.width, small.height);
      const blob = await new Promise<Blob | null>((ok) => small.toBlob(ok, "image/jpeg", 0.75));
      if (!blob) break;
      const res = await fetch(`/api/live/segment?prompts=${encodeURIComponent(PROMPTS.twin.join(","))}`, { method: "POST", body: blob, headers: { "Content-Type": "image/jpeg" } }).catch(() => null);
      if (!res || res.status === 503) {
        gpu = "off";
        load();
        break;
      }
      if (!res.ok) {
        await sleep(res.status === 429 ? 250 : 2000);
        continue;
      }
      if (source !== "twin" || !active) break;
      await paintLive((await res.json()) as Live);
    }
  } finally {
    pumping = false;
    stats();
  }
}

async function segmentPhoto(dataURL: string) {
  const blob = await fetch(dataURL).then((r) => r.blob());
  let res: Response | null = null;
  // The twin's last frame may still be at the GPU right after switching: retry.
  for (let i = 0; i < 8 && source === "river"; i++) {
    res = await fetch(`/api/live/segment?prompts=${encodeURIComponent(PROMPTS.river.join(","))}`, { method: "POST", body: blob, headers: { "Content-Type": "image/jpeg" } }).catch(() => null);
    if (res?.status !== 429) break;
    await sleep(300);
  }
  if (source !== "river") return;
  if (res?.ok) await paintLive((await res.json()) as Live);
  drawLines();
  stats();
}

// --- painting -----------------------------------------------------------------------------
type Bits = { on: Uint8Array; w: number; h: number };
let found: { label: string; n: number }[] = [];
let last: Live | null = null;

// maskBits reads a mask image (a 1-bit white-on-black PNG from the GPU, or the
// service's blue-on-transparent one) as on/off per pixel.
async function maskBits(url: string, w?: number, h?: number): Promise<Bits | null> {
  const img = new Image();
  img.src = url;
  try {
    await img.decode();
  } catch {
    return null;
  }
  w ??= img.naturalWidth;
  h ??= img.naturalHeight;
  const c = new OffscreenCanvas(w, h);
  const g = c.getContext("2d", { willReadFrequently: true })!;
  g.drawImage(img, 0, 0, w, h);
  const px = g.getImageData(0, 0, w, h).data;
  const on = new Uint8Array(w * h);
  for (let i = 0; i < on.length; i++) on[i] = px[i * 4 + 3] > 0 && px[i * 4] + px[i * 4 + 1] + px[i * 4 + 2] > 200 ? 1 : 0;
  return { on, w, h };
}

function sizeTo(c: HTMLCanvasElement, w: number, h: number) {
  if (c.width !== w || c.height !== h) {
    c.width = w;
    c.height = h;
  }
  return c.getContext("2d")!;
}

// smooth draws a mask image onto a layer at the frame's full size through an
// SVG filter in the page (a smooth, light fill with a thin edge). It runs once
// per result, in the canvas: as a CSS filter the browser would redo it on
// every repaint of the moving picture underneath.
const buf = document.createElement("canvas");
function smooth(layer: HTMLCanvasElement, img: ImageData, filter: string) {
  const b = sizeTo(buf, img.width, img.height);
  b.putImageData(img, 0, 0);
  const W = FRAME_W, H = Math.round((img.height * FRAME_W) / img.width);
  const g = sizeTo(layer, W, H);
  g.clearRect(0, 0, W, H);
  g.filter = `url(#${filter})`;
  g.drawImage(buf, 0, 0, W, H);
  g.filter = "none";
}

// paintWater draws every water mask in one translucent blue with a white edge.
function paintWater(masks: Bits[], w: number, h: number) {
  const img = new ImageData(w, h);
  const d = img.data;
  for (const m of masks) {
    for (let i = 0; i < m.on.length; i++) {
      if (!m.on[i]) continue;
      [d[i * 4], d[i * 4 + 1], d[i * 4 + 2]] = WATER_RGB;
      d[i * 4 + 3] = 255;
    }
  }
  smooth(water, img, "sam-smooth-water");
}

async function paintLive(live: Live) {
  const { width: w, height: h, objects } = live.result;
  const masks = await Promise.all(objects.map((o) => (o.mask ? maskBits(o.mask, w, h) : Promise.resolve(null))));
  // Water is a large patch: SAM also returns thin "water" (or "mud") slivers
  // along floating logs, which are not the stream.
  const isWater = (o: Obj) => (o.label === "water" && o.area > 0.01 * w * h) || (o.label === "mud" && o.area > 0.02 * w * h);
  paintWater(masks.filter((m, i): m is Bits => !!m && isWater(objects[i])), w, h);

  // People, logs and vehicles: their own color, a box and a label.
  const img = new ImageData(w, h);
  const d = img.data;
  const isThing = (o: Obj) => !WATER.includes(o.label);
  objects.forEach((o, k) => {
    const m = masks[k];
    if (!m || !isThing(o)) return;
    const [r, gg, b] = COLORS[o.label] ?? [190, 140, 255];
    for (let i = 0; i < m.on.length; i++) {
      if (!m.on[i]) continue;
      d[i * 4] = r;
      d[i * 4 + 1] = gg;
      d[i * 4 + 2] = b;
      d[i * 4 + 3] = 255;
    }
  });
  smooth(things, img, "sam-smooth-things");
  // Boxes and labels at the frame's full size, so the text stays crisp.
  const k = FRAME_W / w;
  const t = sizeTo(tags, Math.round(w * k), Math.round(h * k));
  t.clearRect(0, 0, t.canvas.width, t.canvas.height);
  t.font = "700 15px Inter, system-ui, sans-serif";
  for (const o of objects) {
    if (!isThing(o)) continue;
    const [r, gg, b] = COLORS[o.label] ?? [190, 140, 255];
    const [x0, y0, x1, y1] = o.box.map((v) => v * k);
    t.strokeStyle = `rgb(${r} ${gg} ${b})`;
    t.lineWidth = 2;
    t.strokeRect(x0, y0, x1 - x0, y1 - y0);
    const label = `${o.label} ${Math.round(o.score * 100)}%`;
    const tw = t.measureText(label).width + 12;
    const ly = y0 > 24 ? y0 - 22 : y1 + 2;
    t.fillStyle = `rgb(${r} ${gg} ${b})`;
    t.beginPath();
    t.roundRect(x0, ly, tw, 20, 5);
    t.fill();
    t.fillStyle = "#0b0f18";
    t.fillText(label, x0 + 6, ly + 15);
  }
  const counts = new Map<string, number>();
  for (const o of objects) if (isThing(o)) counts.set(o.label, (counts.get(o.label) ?? 0) + 1);
  found = [...counts].map(([label, n]) => ({ label, n }));

  // The waterline at the gauge, as the live mask shows it (frame pixels).
  liveY = null;
  const c = station?.calibration;
  if (c && source === "twin") {
    const all = masks.filter((m, i): m is Bits => !!m && isWater(objects[i]));
    const s = w / FRAME_W;
    const top = c.top != null ? (c.zero_y - c.top * c.px_per_unit) * s + 4 * s : 0;
    const tops: number[] = [];
    for (let x = Math.round(c.col_x1 * s); x <= Math.round(c.col_x2 * s); x++) {
      for (let y = Math.max(0, Math.ceil(top)); y < h; y++) {
        if (all.some((m) => m.on[y * w + x])) {
          tops.push(y);
          break;
        }
      }
    }
    if (tops.length) liveY = tops.sort((a, b) => a - b)[tops.length >> 1] / s;
  }

  last = live;
  const now = performance.now();
  stamps.push(now);
  while (stamps.length && now - stamps[0] > 4000) stamps.shift();
  shown = stamps.length / 4;
  drawLines();
  stats();
}

function clear() {
  for (const c of [water, things, tags, lines]) c.getContext("2d")!.clearRect(0, 0, c.width, c.height);
  found = [];
  liveY = null;
  last = null;
  stamps.length = 0;
  shown = 0;
}

// Gauge columns (dashed, as tall as the printed gauge), the measured waterline
// (yellow, with the level) and the live one (thin, white), in frame pixels.
function drawLines() {
  const g = sizeTo(lines, FRAME_W, FRAME_H);
  g.clearRect(0, 0, FRAME_W, FRAME_H);
  const c = station?.calibration, r = station?.last;
  if (!c || source !== "twin") return;
  const top = Math.max(0, c.zero_y - ((GAUGE_HEIGHT * 100) / (UNIT_CM[c.unit] ?? 100)) * c.px_per_unit);
  const bottom = Math.min(FRAME_H, c.zero_y);
  g.strokeStyle = "rgba(255,255,255,.95)";
  g.lineWidth = 2.5;
  g.setLineDash([9, 7]);
  g.strokeRect(c.col_x1 - 6, top, c.col_x2 - c.col_x1 + 12, bottom - top);
  g.setLineDash([]);
  const x0 = c.col_x1 - 70, x1 = c.col_x2 + 40;
  if (liveY != null) {
    g.strokeStyle = "rgba(255,255,255,.9)";
    g.lineWidth = 2;
    g.beginPath();
    g.moveTo(x0, liveY);
    g.lineTo(x1, liveY);
    g.stroke();
  }
  if (r?.waterline_y != null && fresh(r)) {
    const y = r.waterline_y;
    g.strokeStyle = "#ffd23f";
    g.lineWidth = 4;
    g.beginPath();
    g.moveTo(x0, y);
    g.lineTo(x1, y);
    g.stroke();
    if (r.level_cm != null) {
      const label = `${Math.round(r.level_cm)} cm`;
      g.font = "700 17px Inter, system-ui, sans-serif";
      const tw = g.measureText(label).width + 16;
      const lx = x1 + 6, ly = y - 14;
      g.fillStyle = "#ffd23f";
      g.beginPath();
      g.roundRect(lx, ly, tw, 28, 14);
      g.fill();
      g.fillStyle = "#111";
      g.fillText(label, lx + 8, ly + 20);
    }
  }
}

// --- the numbers ------------------------------------------------------------------------
function status(msg: string) {
  $("sam-note").textContent = msg;
}

function stats() {
  const live = gpu === "ready" && (source === "river" ? !!last : pumping);
  view.dataset.engine = live ? "live" : "measured";
  $("sam-engine").textContent = live
    ? source === "twin" ? `Live · ${shown.toFixed(1)} frames/s` : "Live GPU on the last photo"
    : gpu === "off" ? "Every 5 s" : gpu === "ready" ? "GPU ready" : `GPU ${gpu}…`;
  const r = station?.last;
  const age = r ? Math.round((Date.now() - Date.parse(r.t)) / 60_000) : 0;
  $("sam-level").textContent =
    (r?.blind ? "camera blind" : r?.level_cm != null ? `${Math.round(r.level_cm)} cm` : "–") + (r && !fresh(r) && source === "twin" ? ` · ${age} min` : "");
  const c = station?.calibration;
  $("sam-live-level").textContent =
    liveY != null && c ? `${Math.round((((c.zero_y - liveY) / c.px_per_unit) * (UNIT_CM[c.unit] ?? 100)))} cm` : "–";
  $("sam-found").textContent = found.length ? found.map((f) => `${f.n} ${NAMES[f.label]?.[f.n === 1 ? 0 : 1] ?? f.label}`).join(" · ") : live ? (source === "twin" ? "nothing yet" : "nothing") : "–";
  $("sam-speed").textContent = last ? `${Math.round(last.result.ms)} ms GPU · ${last.round_trip_ms} ms trip` : r ? `${r.sam_ms} ms (Meta API)` : "–";
  status(
    live
      ? "SAM 3.1 (open weights, on a GPU) looks at this picture as it moves: water in blue, people and logs in their own colors. The level is still measured every 5 s by SAM 3.1 on the Meta Model API: the yellow line."
      : source === "twin"
        ? "Blue is the water SAM 3.1 found on the last measured frame (Meta Model API, every 5 s while the camera node runs). The yellow line is the level it read at the gauge."
        : "Blue is the water SAM 3.1 found on the last photo of the USGS camera. This camera has no gauge: the level comes from how much of the picture is water.",
  );
}

const fresh = (r: Reading) => Date.now() - Date.parse(r.t) < FRESH_MS;
const sleep = (ms: number) => new Promise((ok) => setTimeout(ok, ms));
