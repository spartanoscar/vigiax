import "./sam-view.css";
import { events } from "./events";
import { STATION } from "./station";
import { GAUGE_HEIGHT } from "./world";

// The "SAM view" layer: the last frame a station measured, with the mask SAM
// 3.1 drew on it and the gauge columns and waterline the level came from. Like
// map layers, each can be switched on and off, so a person can see whether the
// segmentation fits the water before trusting (or recalibrating) a station.

type Calibration = { px_per_unit: number; zero_y: number; unit: string; col_x1: number; col_x2: number; source: string };
type Reading = { t: string; level_cm?: number; waterline_y?: number; coverage: number; confidence: number; sam_ms: number; mud?: boolean; blind?: boolean };
type SamView = {
  view: { t: string; prompt: string; objects: number };
  frame: string;
  mask: string;
  station: { calibration?: Calibration; last?: Reading; rating?: object };
};

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const panel = $("sam-view");
const toggle = $<HTMLButtonElement>("sam-toggle");
const frame = $<HTMLImageElement>("sam-frame");
const mask = $<HTMLImageElement>("sam-mask");
const lines = $<HTMLCanvasElement>("sam-lines");
const stage = $("sam-stage");
const UNIT_CM: Record<string, number> = { CM: 1, M: 100, FT: 30.48 };

let station = "twin";
const id = () => (station === "twin" ? STATION : station);
let data: SamView | null = null;

function setOpen(open: boolean) {
  panel.hidden = !open;
  toggle.setAttribute("aria-pressed", String(open));
  document.body.classList.toggle("sam-on", open);
  if (open) load();
}
toggle.addEventListener("click", () => setOpen(panel.hidden));
$("sam-close").addEventListener("click", () => setOpen(false));
panel.addEventListener("click", (e) => e.target === panel && setOpen(false));
addEventListener("keydown", (e) => e.key === "Escape" && !panel.hidden && setOpen(false));

panel.querySelectorAll<HTMLButtonElement>("[data-sam-station]").forEach((b) =>
  b.addEventListener("click", () => {
    station = b.dataset.samStation!;
    panel.querySelectorAll("[data-sam-station]").forEach((x) => x.setAttribute("aria-selected", String(x === b)));
    load();
  }),
);

// Layers: each checkbox shows or hides one of them.
panel.querySelectorAll<HTMLInputElement>("[data-layer]").forEach((c) =>
  c.addEventListener("change", () => stage.classList.toggle(`no-${c.dataset.layer}`, !c.checked)),
);
const opacity = $<HTMLInputElement>("sam-opacity");
const paintOpacity = () => {
  stage.style.setProperty("--mask", opacity.value);
  opacity.style.setProperty("--fill", `${((+opacity.value - +opacity.min) / (+opacity.max - +opacity.min)) * 100}%`);
};
opacity.addEventListener("input", paintOpacity);
paintOpacity();

async function load() {
  const res = await fetch(`/api/stations/${id()}/sam`).catch(() => null);
  if (!res?.ok) {
    data = null;
    stage.classList.add("empty");
    $("sam-empty").hidden = false;
    $("sam-empty").textContent =
      station === "twin" ? "SAM has not measured your twin yet. Start the camera node and this layer fills in with the first frame." : "Waiting for the next photo from the USGS camera (one an hour).";
    for (const k of ["sam-level", "sam-conf", "sam-cover", "sam-prompt", "sam-ms-v"]) $(k).textContent = "–";
    $("sam-time").textContent = "";
    return;
  }
  data = (await res.json()) as SamView;
  stage.classList.remove("empty");
  $("sam-empty").hidden = true;
  frame.src = data.frame;
  mask.src = data.mask;
  frame.decode().then(draw, draw);
  const r = data.station.last;
  const v = data.view;
  $("sam-time").textContent = new Date(v.t).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit", second: "2-digit" });
  $("sam-level").textContent = r?.blind ? "camera blind" : r?.level_cm != null ? `${Math.round(r.level_cm)} cm` : "not measured";
  $("sam-conf").textContent = r ? `${Math.round(r.confidence * 100)} %` : "–";
  $("sam-cover").textContent = r ? `${(r.coverage * 100).toFixed(1)} %` : "–";
  $("sam-prompt").textContent = `"${v.prompt}"${v.objects > 1 ? ` · ${v.objects} areas` : ""}`;
  $("sam-ms-v").textContent = r ? `${r.sam_ms} ms` : "–";
  $("sam-hint").textContent = data.station.calibration
    ? "Blue is what SAM 3.1 calls water on this frame. The level is read where the blue meets the gauge, inside the dashed columns. If the blue misses water or covers something else, recalibrate the gauge."
    : "Blue is what SAM 3.1 calls water on this frame. This camera has no gauge: the level comes from how much of the frame is water, through a rating fitted against the official gage.";
}

// Gauge columns (dashed, as tall as the printed gauge) and the waterline, in
// the frame's own pixels; CSS scales the canvas with the photo.
function draw() {
  if (!data) return;
  const w = frame.naturalWidth, h = frame.naturalHeight;
  lines.width = w;
  lines.height = h;
  const g = lines.getContext("2d")!;
  g.clearRect(0, 0, w, h);
  const c = data.station.calibration, r = data.station.last;
  const px = Math.max(1, w / 640);
  if (c) {
    const top = Math.max(0, c.zero_y - ((GAUGE_HEIGHT * 100) / (UNIT_CM[c.unit] ?? 100)) * c.px_per_unit);
    const bottom = Math.min(h, c.zero_y);
    g.strokeStyle = "rgba(255,255,255,.95)";
    g.lineWidth = 2.5 * px;
    g.setLineDash([9 * px, 7 * px]);
    g.strokeRect(c.col_x1 - 6 * px, top, c.col_x2 - c.col_x1 + 12 * px, bottom - top);
    g.setLineDash([]);
  }
  if (r?.waterline_y != null) {
    const y = r.waterline_y;
    const x0 = c ? c.col_x1 - 40 * px : 0, x1 = c ? c.col_x2 + 40 * px : w;
    g.strokeStyle = "#ffd23f";
    g.lineWidth = 4 * px;
    g.beginPath();
    g.moveTo(x0, y);
    g.lineTo(x1, y);
    g.stroke();
    if (r.level_cm != null) {
      const label = `${Math.round(r.level_cm)} cm`;
      g.font = `700 ${15 * px}px Inter, system-ui, sans-serif`;
      const tw = g.measureText(label).width + 14 * px;
      const lx = Math.min(w - tw - 4, x1 + 6 * px), ly = Math.max(4, y - 13 * px);
      g.fillStyle = "#ffd23f";
      g.beginPath();
      g.roundRect(lx, ly, tw, 26 * px, 13 * px);
      g.fill();
      g.fillStyle = "#111";
      g.fillText(label, lx + 7 * px, ly + 18 * px);
    }
  }
}

// A new frame measured while the layer is open replaces the one on screen.
events.addEventListener("reading", (e) => {
  if (panel.hidden) return;
  const s = JSON.parse((e as MessageEvent).data) as { id: string };
  if (s.id === id()) load();
});
