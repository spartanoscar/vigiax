import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";
import "./style.css";
import { World, GAUGE_POS, GAUGE_HEIGHT, groundHeight, type WorldState } from "./world";
import { CameraNode, FRAME_W, FRAME_H } from "./camera-node";
import { events } from "./events";
import { STATION } from "./station";
import { samView } from "./sam-view";

const FRAME_SECONDS = 5;
const SESSION_MINUTES = 5; // the camera node stops by itself: a surge fits, an open tab costs little

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
$("station-id").textContent = STATION;

// --- scene --------------------------------------------------------------
const world = new World();
const node = new CameraNode(world);

// Kept light on purpose: a Retina screen at full resolution and 60-120 fps
// draws 8 million pixels per frame and heats a laptop for a low-poly valley.
// 1.5x pixels without MSAA looks the same, and 30 fps is enough for water.
const PIXEL_RATIO = Math.min(devicePixelRatio, 1.5);
const renderer = new THREE.WebGLRenderer({ antialias: PIXEL_RATIO < 1.5, powerPreference: "low-power" });
renderer.setPixelRatio(PIXEL_RATIO);
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.shadowMap.enabled = true;
renderer.shadowMap.autoUpdate = false; // trees, rocks and the gauge never move: one shadow pass
renderer.shadowMap.needsUpdate = true;
$("view").appendChild(renderer.domElement);
const mainEnv = world.reflection(renderer);

const camera = new THREE.PerspectiveCamera(50, 1, 0.1, 600);
camera.position.set(GAUGE_POS.x - 4, 7, GAUGE_POS.z + 22);
const controls = new OrbitControls(camera, renderer.domElement);
controls.target.set(GAUGE_POS.x - 2, 1, GAUGE_POS.z - 6);
controls.maxPolarAngle = Math.PI * 0.48;
controls.enableDamping = true;

// The node itself, so viewers see where frames come from: a pole from the
// bank, the camera housing with its lens, and a small solar panel. It lives on
// layer 1, which only the main view draws: the node must not film itself.
const NODE_LAYER = 1;
camera.layers.enable(NODE_LAYER);
const nodeModel = new THREE.Group();
{
  const metal = new THREE.MeshStandardMaterial({ color: 0x6b7178, roughness: 0.6, metalness: 0.4 });
  const housing = new THREE.MeshStandardMaterial({ color: 0xe8e4da, roughness: 0.5 });
  const dark = new THREE.MeshStandardMaterial({ color: 0x15191f, roughness: 0.3 });
  const p = node.camera.position;
  const foot = groundHeight(p.x, p.z);
  const pole = new THREE.Mesh(new THREE.CylinderGeometry(0.05, 0.07, p.y - foot, 8), metal);
  pole.position.set(p.x, (p.y + foot) / 2 - 0.1, p.z);
  const head = new THREE.Group();
  head.position.copy(p);
  head.quaternion.copy(node.camera.quaternion);
  const box = new THREE.Mesh(new THREE.BoxGeometry(0.22, 0.18, 0.42), housing);
  const lens = new THREE.Mesh(new THREE.CylinderGeometry(0.06, 0.06, 0.05, 16), dark);
  lens.rotation.x = Math.PI / 2;
  lens.position.z = -0.23;
  const hood = new THREE.Mesh(new THREE.BoxGeometry(0.26, 0.02, 0.48), housing);
  hood.position.y = 0.11;
  head.add(box, lens, hood);
  const panel = new THREE.Mesh(new THREE.BoxGeometry(0.5, 0.03, 0.36), dark);
  panel.position.set(p.x, p.y + 0.45, p.z + 0.1);
  panel.rotation.x = -0.5;
  nodeModel.add(pole, head, panel);
  nodeModel.traverse((o) => {
    o.layers.set(NODE_LAYER);
    o.castShadow = true;
  });
}
world.scene.add(nodeModel);

function resize() {
  const el = $("view");
  renderer.setSize(el.clientWidth, el.clientHeight);
  camera.aspect = el.clientWidth / el.clientHeight;
  camera.updateProjectionMatrix();
}
addEventListener("resize", resize);
resize();

// --- twin state ---------------------------------------------------------
const state: WorldState = { levelM: 0.45, mud: 0.15, rain: 0, night: 0, people: 0, logs: false, debrisFlow: false, dam: false };
let surge: { from: number; to: number; mudFrom: number; t0: number; secs: number } | null = null;

const levelInput = $<HTMLInputElement>("level");
const mudInput = $<HTMLInputElement>("mud");
function showControls() {
  levelInput.value = String(state.levelM);
  mudInput.value = String(state.mud);
  $("level-out").textContent = `${state.levelM.toFixed(2)} m`;
  $("mud-out").textContent = `${Math.round(state.mud * 100)} %`;
  $("true-level").textContent = `${(state.levelM * 100).toFixed(0)} cm`;
}
levelInput.oninput = () => { surge = null; state.levelM = +levelInput.value; showControls(); };
mudInput.oninput = () => { state.mud = +mudInput.value; showControls(); };
$("surge").onclick = () => {
  const secs = +$<HTMLSelectElement>("surge-min").value * 60;
  surge = { from: state.levelM, to: 3.2, mudFrom: state.mud, t0: performance.now(), secs };
  state.logs = check("logs").checked = true; // a flash flood carries what it tears from the banks
};
showControls();

// --- scene and camera conditions: what the field throws at a camera --------
const check = (id: string) => $<HTMLInputElement>(id);
const bind = (id: string, apply: (el: HTMLInputElement) => void) => {
  const el = check(id);
  el.addEventListener("input", () => apply(el));
  apply(el);
};
bind("people", (el) => (state.people = +el.value));
bind("logs", (el) => (state.logs = el.checked));
bind("debris-flow", (el) => (state.debrisFlow = el.checked));

// Damming, then release: how many debris flows begin. A jam of logs and rocks
// closes the channel upstream and the stream drops; after a deceptive calm the
// dam gives way and a debris flow comes down. Vigiax should warn at the drop.
let damming: { t0: number; from: number } | null = null;
const DROP_S = 60, CALM_S = 45, RELEASE_S = 60;
$("damming").onclick = () => {
  surge = null;
  damming = { t0: performance.now(), from: Math.max(state.levelM, 0.45) };
  state.dam = true;
  state.debrisFlow = check("debris-flow").checked = false;
};
function stepDamming() {
  if (!damming) return;
  const s = (performance.now() - damming.t0) / 1000;
  const low = Math.max(0.22, damming.from * 0.35);
  if (s < DROP_S) {
    const k = s / DROP_S;
    state.levelM = damming.from + (low - damming.from) * k * k * (3 - 2 * k);
  } else if (s < DROP_S + CALM_S) {
    state.levelM = low;
  } else {
    state.dam = false;
    state.debrisFlow = check("debris-flow").checked = true;
    surge = { from: low, to: 3.2, mudFrom: state.mud, t0: performance.now(), secs: RELEASE_S };
    damming = null;
  }
  showControls();
}
bind("rain", (el) => (state.rain = +el.value));
bind("night", (el) => (state.night = +el.value));
bind("lens-drops", (el) => (node.lensState.drops = el.checked));
bind("lens-haze", (el) => (node.lensState.haze = +el.value));
bind("shake", (el) => (node.shake = +el.value));
bind("infrared", (el) => (node.infrared = el.checked));
const knock = $<HTMLButtonElement>("knock");
knock.onclick = () => {
  if (node.isKnocked) node.fix();
  else node.knock();
  knock.textContent = node.isKnocked ? "Put the camera back" : "Knock the camera";
};

// --- camera node --------------------------------------------------------
type Reading = { t: string; level_cm?: number; waterline_y?: number; coverage: number; confidence: number; sam_ms: number };
type Summary = {
  id: string;
  calibration?: { source: string; px_per_unit: number; zero_y: number; unit: string; col_x1: number; col_x2: number };
  last?: Reading;
  rise_5min_cm_per_min?: number;
};

let sending = false;
let timer = 0;
let sessionEnds = 0;
const truth: { t: number; cm: number }[] = []; // what the twin really had when each frame left
const measured: { t: number; cm: number }[] = [];

async function sendFrame() {
  // A hidden tab stops animating: its frames would show water that no longer
  // moves, and the station would measure (and pay for) a frozen stream.
  if (document.hidden) {
    $("send-state").textContent = "Paused while the tab is hidden";
    return;
  }
  const blob = await node.jpeg();
  truth.push({ t: Date.now(), cm: state.levelM * 100 });
  $("send-state").textContent = "Sending frame…";
  const res = await fetch(`/api/stations/${STATION}/frames`, { method: "POST", body: blob, headers: { "Content-Type": "image/jpeg" } });
  const body = await res.json();
  if (!res.ok) {
    if (res.status === 503) setSending(false, body.error); // out of budget: stop asking
    else $("send-state").textContent = body.error ?? `Error ${res.status}`;
    return;
  }
  $("send-state").textContent = `Frame sent ${new Date().toLocaleTimeString("en-US")}`;
  show(body as Summary);
}

function setSending(on: boolean, note = "Camera node idle") {
  sending = on;
  clearInterval(timer);
  if (on) {
    sessionEnds = Date.now() + SESSION_MINUTES * 60_000;
    const tick = () => {
      const left = Math.ceil((sessionEnds - Date.now()) / 1000);
      if (left <= 0) return setSending(false, `Camera node stopped after ${SESSION_MINUTES} min`);
      $("send").textContent = `Stop camera node (${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")})`;
    };
    sendFrame().catch(showError);
    timer = window.setInterval(() => {
      tick();
      if (sending) sendFrame().catch(showError);
    }, FRAME_SECONDS * 1000);
    tick();
  } else {
    $("send").textContent = "Start camera node";
    $("send-state").textContent = note;
  }
}
$("send").onclick = () => setSending(!sending);

// Calibrate by reading the gauge (SAM 3.1 + Muse Spark); if the model check
// rejects it, fall back to the twin's known geometry.
$("calibrate").onclick = async () => {
  $("calib-state").textContent = "Reading the gauge with Muse Spark…";
  const res = await fetch(`/api/stations/${STATION}/calibration`, { method: "POST", body: await node.jpeg(), headers: { "Content-Type": "image/jpeg" } });
  const body = await res.json();
  if (res.ok) return show(body as Summary);
  $("calib-state").textContent = `Gauge reading rejected (${body.error}); using the twin's geometry.`;
  await calibrateFromGeometry();
};

async function calibrateFromGeometry() {
  const lo = node.project(world.gaugePoint(0.5)), hi = node.project(world.gaugePoint(3.5));
  const left = node.project(world.gaugePoint(1, -1)), right = node.project(world.gaugePoint(1, 1));
  const res = await fetch(`/api/stations/${STATION}/calibration`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ Y1: hi.y, V1: 3.5, Y2: lo.y, V2: 0.5, Unit: "M", X1: Math.round(Math.min(left.x, right.x)), X2: Math.round(Math.max(left.x, right.x)), Top: GAUGE_HEIGHT }),
  });
  show((await res.json()) as Summary);
}

function show(s: Summary) {
  if (s.calibration) {
    const c = s.calibration;
    $("calib-state").textContent = `Calibrated (${c.source === "labels" ? "gauge read by Muse Spark" : "twin geometry"}): ${c.px_per_unit.toFixed(1)} px per ${c.unit.toLowerCase()}`;
  }
  const r = s.last;
  if (!r) return;
  $("sam-ms").textContent = `SAM ${r.sam_ms} ms`;
  $("measured-level").textContent =
    r.level_cm != null ? `${r.level_cm.toFixed(0)} cm`
    : r.waterline_y != null ? "not calibrated"
    : r.coverage > 0 ? "no water at the gauge"
    : "no water found";
  $("rise").textContent = s.rise_5min_cm_per_min != null ? `${(s.rise_5min_cm_per_min * 10).toFixed(1)} cm / 10 min` : "–";
  if (r.level_cm != null) {
    const t = Date.parse(r.t);
    if (!measured.some((m) => m.t === t)) measured.push({ t, cm: r.level_cm });
  }
  drawOverlay(r.waterline_y, s.calibration);
  drawChart();
}

function showError(e: unknown) {
  $("send-state").textContent = e instanceof Error ? e.message : String(e);
}

// Stay in sync with readings sent from other tabs or devices.
events.addEventListener("reading", (e) => {
  const s = JSON.parse((e as MessageEvent).data) as Summary;
  if (s.id === STATION) show(s);
});
fetch(`/api/stations/${STATION}`)
  .then((r) => (r.ok ? (r.json() as Promise<Summary>) : undefined))
  .then((s) => {
    if (s) show(s);
    // The twin knows its own geometry: refresh that calibration on every load
    // (free), but keep one read from the gauge by Muse Spark.
    if (s?.calibration?.source !== "labels") calibrateFromGeometry().catch(showError);
  })
  .catch(() => ($("send-state").textContent = "Station service unreachable"));
const showCost = () =>
  fetch("/api/stations").then((r) => r.json()).then((b) => ($("cost").textContent = `$${b.spent_today_usd.toFixed(3)}`)).catch(() => {});
showCost();
setInterval(showCost, 15000);

// --- feed and chart -------------------------------------------------------
const feed = $<HTMLCanvasElement>("feed");
const overlay = $<HTMLCanvasElement>("feed-overlay");
feed.width = overlay.width = FRAME_W;
feed.height = overlay.height = FRAME_H;
const feedCtx = feed.getContext("2d")!;
const UNIT_CM: Record<string, number> = { CM: 1, M: 100, FT: 30.48 };

function drawOverlay(y: number | undefined, c?: Summary["calibration"]) {
  const g = overlay.getContext("2d")!;
  g.clearRect(0, 0, FRAME_W, FRAME_H);
  if (c) {
    // The columns where the waterline is read, only as tall as the printed gauge.
    const top = Math.max(0, c.zero_y - (GAUGE_HEIGHT * 100 / (UNIT_CM[c.unit] ?? 100)) * c.px_per_unit);
    const bottom = Math.min(FRAME_H, c.zero_y);
    g.strokeStyle = "rgba(255,255,255,.9)";
    g.lineWidth = 3;
    g.setLineDash([10, 8]);
    g.strokeRect(c.col_x1 - 6, top, c.col_x2 - c.col_x1 + 12, bottom - top);
    g.setLineDash([]);
  }
  if (y == null) return;
  g.strokeStyle = "#00beff";
  g.lineWidth = 6;
  g.beginPath();
  g.moveTo(0, y);
  g.lineTo(FRAME_W, y);
  g.stroke();
}

const chart = $<HTMLCanvasElement>("chart");
function drawChart() {
  const w = (chart.width = chart.clientWidth * devicePixelRatio);
  const h = (chart.height = 160 * devicePixelRatio);
  const g = chart.getContext("2d")!;
  g.clearRect(0, 0, w, h);
  const now = Date.now(), span = 10 * 60 * 1000, maxCm = 380;
  const X = (t: number) => ((t - (now - span)) / span) * w;
  const Y = (cm: number) => h - (cm / maxCm) * (h - 8) - 4;
  const css = getComputedStyle(document.documentElement);
  g.strokeStyle = css.getPropertyValue("--grid");
  g.lineWidth = 1;
  g.fillStyle = css.getPropertyValue("--muted");
  g.font = `${11 * devicePixelRatio}px ui-monospace, Menlo, monospace`;
  for (let cm = 0; cm <= 300; cm += 100) {
    g.beginPath(); g.moveTo(0, Y(cm)); g.lineTo(w, Y(cm)); g.stroke();
    g.fillText(`${cm / 100} m`, 4, Y(cm) - 3);
  }
  g.strokeStyle = css.getPropertyValue("--truth");
  g.lineWidth = 2 * devicePixelRatio;
  g.beginPath();
  truth.filter((p) => p.t > now - span).forEach((p, i) => (i ? g.lineTo(X(p.t), Y(p.cm)) : g.moveTo(X(p.t), Y(p.cm))));
  g.stroke();
  g.fillStyle = "#00beff";
  for (const p of measured.filter((p) => p.t > now - span)) {
    g.beginPath(); g.arc(X(p.t), Y(p.cm), 3.5 * devicePixelRatio, 0, Math.PI * 2); g.fill();
  }
}

// --- loop -------------------------------------------------------------------
const clock = new THREE.Clock();
const FRAME_MS = 1000 / 30;
let feedAt = 0,
  frameAt = 0;
renderer.setAnimationLoop((now) => {
  if (now - frameAt < FRAME_MS - 2) return; // 30 fps, also on 120 Hz screens
  frameAt = now;
  const dt = clock.getDelta();
  stepDamming();
  if (surge) {
    const k = Math.min(1, (performance.now() - surge.t0) / 1000 / surge.secs);
    const e = k * k * (3 - 2 * k); // slow start, fast middle: how a flood front arrives
    state.levelM = surge.from + (surge.to - surge.from) * e;
    state.mud = Math.max(surge.mudFrom, Math.min(1, surge.mudFrom + e * 1.2));
    if (k >= 1) surge = null;
    showControls();
  }
  world.update(state, dt);
  controls.update();
  // In the SAM view the screen shows the camera node's picture, at full rate,
  // and the main view is not drawn at all.
  const sam = samView.twin;
  if (sam) {
    node.render();
    samView.frame(node.output);
  } else if (!samView.open) {
    world.setWaterEnvironment(mainEnv);
    renderer.toneMappingExposure = world.exposure;
    renderer.render(world.scene, camera);
  }
  if (performance.now() - feedAt > 500) {
    feedAt = performance.now();
    if (!sam) node.render();
    feedCtx.drawImage(node.output, 0, 0);
    drawChart();
  }
});
