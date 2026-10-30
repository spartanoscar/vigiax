import "./sam-view.css";
import { events } from "./events";
import { STATION } from "./station";
import { GAUGE_HEIGHT } from "./world";
import { FRAME_W, FRAME_H } from "./camera-node";
import { link } from "./network";

// The "SAM view", the twin's first view: the screen shows the camera node's
// picture, with what SAM 3.1 finds on it drawn on top, like layers on a map: the
// water, the people and logs, and the gauge with the waterline. The real river's
// card (El Reno) gets the same layers on its last photo.
//
// With a GPU linked to the service, open-weights SAM 3.1 looks at the twin's
// picture a few times a second and at each new photo of the river; without one,
// both show the mask of the last frame measured through the Meta Model API (every
// 5 s while the camera node runs, every hour on the river). Either way the level
// itself comes from that measurement, never from this view. The 3D view, to walk
// around the valley, is one button away.

type Calibration = { px_per_unit: number; zero_y: number; unit: string; col_x1: number; col_x2: number; top?: number };
type Reading = { t: string; level_cm?: number; waterline_y?: number; coverage: number; confidence: number; sam_ms: number; blind?: boolean };
type Summary = { id: string; calibration?: Calibration; last?: Reading; rating?: object };
type Measured = { view: { t: string; prompt: string; objects: number }; frame: string; mask: string; station: Summary };
type Obj = { label: string; box: [number, number, number, number]; score: number; area: number; mask?: string };
type Live = { result: { width: number; height: number; ms: number; objects: Obj[] }; round_trip_ms: number };
type Layers = { water: HTMLCanvasElement; things: HTMLCanvasElement; tags: HTMLCanvasElement };

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const toggle = $<HTMLButtonElement>("sam-toggle");
const view = $("sam-view");
const video = $<HTMLCanvasElement>("sam-video");
const lines = $<HTMLCanvasElement>("sam-lines");
const twinLayers: Layers = { water: $("sam-water"), things: $("sam-things"), tags: $("sam-tags") };
const riverLayers: Layers = { water: $("live-water"), things: $("live-things"), tags: $("live-tags") };
video.width = FRAME_W;
video.height = FRAME_H;
const videoCtx = video.getContext("2d")!;

const UNIT_CM: Record<string, number> = { CM: 1, M: 100, FT: 30.48 };
const RIVER = "el-reno";
// What to look for. "water" finds the twin's stream and the real river alike;
// on real rivers "mud" adds a debris flow, which SAM does not call water (on the
// twin it finds the logs instead, so it is not asked there).
const WATER = ["water", "mud"];
const PROMPTS = { twin: ["water", "person", "log"], river: [...WATER, "person", "vehicle", "log"] };
const FRESH_MS = 60_000; // a measured level older than this is not drawn as the current one
const COLORS: Record<string, [number, number, number]> = { person: [255, 72, 140], log: [255, 176, 32], vehicle: [120, 255, 110] };
const NAMES: Record<string, [string, string]> = { person: ["person", "people"], log: ["log", "logs"], vehicle: ["vehicle", "vehicles"] };
const WATER_RGB: [number, number, number] = [24, 96, 255];

let active = true; // the SAM view is where the twin starts
let gpu = "off";
let station: Summary | null = null;
let liveY: number | null = null; // waterline the live mask shows, in frame pixels
let pumping = false;
let found: { label: string; n: number }[] = [];
let last: Live | null = null;
const stamps: number[] = []; // when live results arrived, for the frame rate

// --- SAM view or 3D view ----------------------------------------------------------
function setActive(on: boolean) {
  active = on;
  view.hidden = !on;
  toggle.setAttribute("aria-pressed", String(on));
  $("sam-toggle-t").textContent = on ? "3D view" : "SAM view";
  toggle.title = on ? "Walk around the valley in 3D" : "Back to what the camera and SAM 3.1 see";
  document.body.classList.toggle("sam-on", on);
  if (on && gpu === "ready") pump();
}
toggle.addEventListener("click", () => setActive(!active));
document.querySelectorAll<HTMLInputElement>("[data-layer]").forEach((c) =>
  c.addEventListener("change", () => document.body.classList.toggle(`no-${c.dataset.layer}`, !c.checked)),
);

// Without signal (or on LoRa) nothing reaches SAM 3.1: its masks would be the
// last frame it saw, frozen while the water rises. They step aside, and the
// node's own reading of the gauge is drawn instead.
let nodeY: number | null = null, nodeCm: number | null = null;
addEventListener("net-change", () => {
  if (link.online) {
    nodeY = nodeCm = null;
    if (active && gpu === "ready") pump();
    loadTwin();
  }
  drawLines();
  stats();
});

// main.ts asks whether to draw the camera's picture instead of the 3D view, and
// hands each frame over as it renders it.
export const samView = {
  // local: the waterline the node read by itself, while it has no signal.
  local(y?: number, cm?: number) {
    nodeY = y ?? null;
    nodeCm = cm ?? null;
    drawLines();
    stats();
  },
  get open() {
    return active;
  },
  get twin() {
    return active;
  },
  frame(src: HTMLCanvasElement) {
    videoCtx.drawImage(src, 0, 0);
  },
};

// --- the measured frames (Meta Model API) -------------------------------------------
async function loadTwin() {
  const [res, sum] = await Promise.all([
    fetch(`/api/stations/${STATION}/sam`).catch(() => null),
    fetch(`/api/stations/${STATION}`).then((r) => (r.ok ? (r.json() as Promise<Summary>) : null)).catch(() => null),
  ]);
  station = sum;
  const m = res?.ok ? ((await res.json()) as Measured) : null;
  if (m) station = { ...m.station, id: STATION };
  // Without a GPU running, the mask SAM drew on the measured frame.
  if (m && !pumping) {
    const bits = await maskBits(m.mask);
    if (bits) paint(twinLayers, [bits], [], bits.w, bits.h);
    found = [];
    liveY = null;
  }
  drawLines();
  stats();
}

// The river: the mask of its last measured photo, and with a GPU what else is
// on it (people, vehicles, logs), once per photo.
let riverPhoto = "";
async function loadRiver() {
  const res = await fetch(`/api/stations/${RIVER}/sam`).catch(() => null);
  if (!res?.ok) return;
  const m = (await res.json()) as Measured;
  riverPhoto = m.frame;
  const bits = await maskBits(m.mask);
  if (bits) paint(riverLayers, [bits], [], bits.w, bits.h);
  if (gpu === "ready") await segmentRiver();
}

async function segmentRiver() {
  if (!riverPhoto) return;
  const blob = await fetch(riverPhoto).then((r) => r.blob());
  for (let i = 0; i < 8; i++) {
    // The twin's last frame may still be at the GPU: one at a time per visitor.
    const res = await fetch(`/api/live/segment?prompts=${encodeURIComponent(PROMPTS.river.join(","))}`, { method: "POST", body: blob, headers: { "Content-Type": "image/jpeg" } }).catch(() => null);
    if (res?.ok) return paintLive(riverLayers, (await res.json()) as Live, false);
    if (res?.status !== 429) return;
    await sleep(300);
  }
}

events.addEventListener("reading", (e) => {
  const s = JSON.parse((e as MessageEvent).data) as Summary;
  if (s.id === STATION) loadTwin();
  else if (s.id === RIVER) loadRiver();
});

// --- the GPU ----------------------------------------------------------------------------
let gpuTimer = 0;
type GPUState = { stage: string; minutes_left?: number; can_control?: boolean; starting_s?: number; note?: string };
let gpuState: GPUState = { stage: "off" };
// checkGPU asks how the GPU is (and, while the SAM view is open, says a page is
// watching, so it stays on): every few seconds while it starts, every 15 s otherwise.
async function checkGPU() {
  clearTimeout(gpuTimer);
  const h: GPUState = await fetch(`/api/gpu${active && !document.hidden ? "?beat=1" : ""}`)
    .then((r) => r.json())
    .catch(() => ({ stage: "off" }));
  const was = gpu;
  gpu = h.stage;
  gpuState = h;
  showGPU();
  stats();
  if (gpu === "ready") {
    if (active && link.online) pump();
    if (was !== "ready") segmentRiver();
  } else if (was === "ready") {
    loadTwin();
    loadRiver();
  }
  gpuTimer = window.setTimeout(checkGPU, gpu === "ready" || gpu === "off" ? 15_000 : 3_000);
}

const gpuBox = $("sam-gpu");
const gpuBtn = $<HTMLButtonElement>("sam-gpu-btn");
const unlock = $<HTMLFormElement>("sam-gpu-unlock");
function showGPU() {
  const h = gpuState;
  const on = h.stage === "ready";
  const starting = !on && h.stage !== "off" && h.stage !== "error";
  gpuBox.dataset.stage = on ? "ready" : starting ? "starting" : "off";
  const s = h.starting_s ?? 0;
  $("sam-gpu-text").textContent = on
    ? `Live GPU on · ${Math.max(0, Math.floor(h.minutes_left ?? 0))} min left`
    : starting
      ? `Starting the GPU… ${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
      : "Live GPU off";
  gpuBtn.hidden = !h.can_control;
  gpuBtn.disabled = false;
  gpuBtn.textContent = on ? "Turn off" : starting ? "Cancel" : "Turn on";
  unlock.hidden = !!h.can_control || on || starting;
  const note = $("sam-gpu-note");
  note.textContent =
    h.note ||
    (on || starting
      ? ""
      : h.can_control
        ? "Rents a GPU for open-weights SAM 3.1 (about 35 s to start): the masks then move with the water. It turns itself off 3 min after nobody watches."
        : "Judges: your code turns on a GPU and the masks move with the water, live. Without it they update every 5 s.");
  note.hidden = !note.textContent;
}

gpuBtn.addEventListener("click", async () => {
  const on = gpuBox.dataset.stage === "off";
  gpuBtn.disabled = true;
  if (on) gpuState = { ...gpuState, stage: "starting", starting_s: 0, note: "" };
  else {
    gpuState = { ...gpuState, stage: "off" };
    gpu = "off";
  }
  showGPU();
  const res = await fetch("/api/gpu", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ on }) }).catch(() => null);
  if (res && res.ok) gpuState = await res.json();
  if (!on) {
    loadTwin();
    loadRiver();
  }
  checkGPU();
});

unlock.addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = $<HTMLInputElement>("sam-gpu-code");
  const res = await fetch("/api/access", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ code: input.value }) }).catch(() => null);
  input.value = "";
  if (!res?.ok) {
    $("sam-gpu-note").hidden = false;
    $("sam-gpu-note").textContent = res ? ((await res.json()).error ?? "That code did not work.") : "The service is not reachable.";
    return;
  }
  checkGPU();
});

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
    while (active && gpu === "ready" && link.online) {
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
        break;
      }
      if (!res.ok) {
        await sleep(res.status === 429 ? 250 : 2000);
        continue;
      }
      if (!active) break;
      await paintLive(twinLayers, (await res.json()) as Live, true);
    }
  } finally {
    pumping = false;
    stamps.length = 0;
    if (gpu !== "ready") loadTwin();
    stats();
  }
}

// --- painting -----------------------------------------------------------------------------
type Bits = { on: Uint8Array; w: number; h: number };

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

// paint fills the water masks in translucent blue with a white edge, and the
// people, logs and vehicles in their own color with a box and a label.
function paint(layers: Layers, waterMasks: Bits[], items: { o: Obj; m: Bits }[], w: number, h: number) {
  const wimg = new ImageData(w, h);
  for (const m of waterMasks) {
    for (let i = 0; i < m.on.length; i++) {
      if (!m.on[i]) continue;
      [wimg.data[i * 4], wimg.data[i * 4 + 1], wimg.data[i * 4 + 2]] = WATER_RGB;
      wimg.data[i * 4 + 3] = 255;
    }
  }
  smooth(layers.water, wimg, "sam-smooth-water");

  const timg = new ImageData(w, h);
  for (const { o, m } of items) {
    const [r, g, b] = COLORS[o.label] ?? [190, 140, 255];
    for (let i = 0; i < m.on.length; i++) {
      if (!m.on[i]) continue;
      timg.data[i * 4] = r;
      timg.data[i * 4 + 1] = g;
      timg.data[i * 4 + 2] = b;
      timg.data[i * 4 + 3] = 255;
    }
  }
  smooth(layers.things, timg, "sam-smooth-things");

  // Boxes and labels at the size they are shown, so the text stays crisp.
  const W = Math.max(1, Math.round(layers.tags.clientWidth * devicePixelRatio)), H = Math.round((W * h) / w);
  const t = sizeTo(layers.tags, W, H);
  t.clearRect(0, 0, W, H);
  const k = W / w, px = devicePixelRatio;
  t.font = `700 ${12 * px}px Inter, system-ui, sans-serif`;
  for (const { o } of items) {
    const [r, g, b] = COLORS[o.label] ?? [190, 140, 255];
    const [x0, y0, x1, y1] = o.box.map((v) => v * k);
    t.strokeStyle = `rgb(${r} ${g} ${b})`;
    t.lineWidth = 1.5 * px;
    t.strokeRect(x0, y0, x1 - x0, y1 - y0);
    const label = `${o.label} ${Math.round(o.score * 100)}%`;
    const tw = t.measureText(label).width + 10 * px;
    const ly = y0 > 20 * px ? y0 - 18 * px : y1 + 2 * px;
    t.fillStyle = `rgb(${r} ${g} ${b})`;
    t.beginPath();
    t.roundRect(x0, ly, tw, 16 * px, 4 * px);
    t.fill();
    t.fillStyle = "#0b0f18";
    t.fillText(label, x0 + 5 * px, ly + 12 * px);
  }
}

async function paintLive(layers: Layers, live: Live, twin: boolean) {
  const { width: w, height: h, objects } = live.result;
  const masks = await Promise.all(objects.map((o) => (o.mask ? maskBits(o.mask, w, h) : Promise.resolve(null))));
  // Water is a large patch: SAM also returns thin "water" (or "mud") slivers
  // along floating logs, which are not the stream.
  const isWater = (o: Obj) => (o.label === "water" && o.area > 0.01 * w * h) || (o.label === "mud" && o.area > 0.02 * w * h);
  const waterMasks = masks.filter((m, i): m is Bits => !!m && isWater(objects[i]));
  const items = objects.flatMap((o, i) => (masks[i] && !WATER.includes(o.label) ? [{ o, m: masks[i]! }] : []));
  paint(layers, waterMasks, items, w, h);
  if (!twin) return;

  const counts = new Map<string, number>();
  for (const { o } of items) counts.set(o.label, (counts.get(o.label) ?? 0) + 1);
  found = [...counts].map(([label, n]) => ({ label, n }));

  // The waterline at the gauge, as the live mask shows it (frame pixels).
  liveY = null;
  const c = station?.calibration;
  if (c) {
    const s = w / FRAME_W;
    const top = c.top != null ? (c.zero_y - c.top * c.px_per_unit) * s + 4 * s : 0;
    const tops: number[] = [];
    for (let x = Math.round(c.col_x1 * s); x <= Math.round(c.col_x2 * s); x++) {
      for (let y = Math.max(0, Math.ceil(top)); y < h; y++) {
        if (waterMasks.some((m) => m.on[y * w + x])) {
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
  drawLines();
  stats();
}

// Gauge columns (dashed, as tall as the printed gauge), the measured waterline
// (yellow, with the level) and the live one (thin, white), in frame pixels.
function drawLines() {
  const g = sizeTo(lines, FRAME_W, FRAME_H);
  g.clearRect(0, 0, FRAME_W, FRAME_H);
  const c = station?.calibration, r = station?.last;
  if (!c) return;
  const top = Math.max(0, c.zero_y - ((GAUGE_HEIGHT * 100) / (UNIT_CM[c.unit] ?? 100)) * c.px_per_unit);
  const bottom = Math.min(FRAME_H, c.zero_y);
  g.strokeStyle = "rgba(255,255,255,.95)";
  g.lineWidth = 2.5;
  g.setLineDash([9, 7]);
  g.strokeRect(c.col_x1 - 6, top, c.col_x2 - c.col_x1 + 12, bottom - top);
  g.setLineDash([]);
  const x0 = c.col_x1 - 70, x1 = c.col_x2 + 40;
  if (!link.online) {
    // The node's own reading, in its colour, with where it comes from.
    if (nodeY == null) return;
    g.strokeStyle = "#ffb020";
    g.lineWidth = 4;
    g.beginPath();
    g.moveTo(x0, nodeY);
    g.lineTo(x1, nodeY);
    g.stroke();
    const label = `${Math.round(nodeCm ?? 0)} cm · on the node`;
    g.font = "700 17px Inter, system-ui, sans-serif";
    const tw = g.measureText(label).width + 16;
    // On the gauge's left: the camera card covers the right of the picture.
    const lx = x0 - tw - 8;
    g.fillStyle = "#ffb020";
    g.beginPath();
    g.roundRect(lx, nodeY - 14, tw, 28, 14);
    g.fill();
    g.fillStyle = "#111";
    g.fillText(label, lx + 8, nodeY + 6);
    return;
  }
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
function stats() {
  if (!link.online) {
    document.body.dataset.sam = "paused";
    $("sam-engine").textContent = link.mode === "lora" ? "paused · LoRa carries numbers only" : "paused · no signal";
    $("sam-live-level").textContent = nodeCm != null ? `${Math.round(nodeCm)} cm (node)` : "–";
    $("sam-found").textContent = "–";
    $("sam-speed").textContent = "–";
    $("sam-note").textContent =
      "No picture reaches SAM 3.1 now, so its masks are hidden. The orange line is the node reading the gauge by itself, with what it learned from SAM 3.1 while it had signal.";
    return;
  }
  const live = gpu === "ready" && pumping;
  document.body.dataset.sam = live ? "live" : "measured";
  $("sam-engine").textContent = live ? `live · ${(stamps.length / 4).toFixed(1)} frames/s` : gpu === "off" || gpu === "error" ? "every 5 s" : "GPU starting…";
  const c = station?.calibration;
  $("sam-live-level").textContent = liveY != null && c ? `${Math.round(((c.zero_y - liveY) / c.px_per_unit) * (UNIT_CM[c.unit] ?? 100))} cm` : "–";
  $("sam-found").textContent = found.length ? found.map((f) => `${f.n} ${NAMES[f.label]?.[f.n === 1 ? 0 : 1] ?? f.label}`).join(" · ") : live ? "nothing" : "–";
  const r = station?.last;
  $("sam-speed").textContent = live && last ? `${Math.round(last.result.ms)} ms GPU · ${last.round_trip_ms} ms trip` : r ? `${r.sam_ms} ms per frame (Meta Model API)` : "–";
  $("sam-note").textContent = live
    ? "Open-weights SAM 3.1 on a GPU looks at the picture as it moves. The level is still measured every 5 s by SAM 3.1 on the Meta Model API: the yellow line."
    : "Blue is the water SAM 3.1 (Meta Model API) found on the last frame, every 5 s. The yellow line is the level it read at the gauge.";
}

const fresh = (r: Reading) => Date.now() - Date.parse(r.t) < FRESH_MS;
const sleep = (ms: number) => new Promise((ok) => setTimeout(ok, ms));

setActive(true);
loadTwin();
loadRiver();
checkGPU();
