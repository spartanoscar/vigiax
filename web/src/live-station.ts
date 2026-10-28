// The real river next to the twin: a USGS webcam on the North Canadian River
// at El Reno, Oklahoma. The server measures each new photo with SAM 3.1 and
// stores the official gage reading beside it; this card only shows them.

import { events } from "./events";

const ID = "el-reno";
const DAY_MIN = 1440;

type Reading = { t: string; level_cm?: number; ref_cm?: number; blind?: boolean; frame_url?: string };
type Summary = { id: string; last?: Reading; rise_3h_cm_per_min?: number };

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const card = $("live");
const toggle = $<HTMLButtonElement>("live-toggle");
const photo = $<HTMLImageElement>("live-photo");
const chart = $<HTMLCanvasElement>("live-chart");

let readings: Reading[] = [];

// --- open / closed ----------------------------------------------------------
// Open by default where there is room beside the 3D view; a pill otherwise.
const KEY = "vigiax-live-open";
const stored = (() => { try { return localStorage.getItem(KEY); } catch { return null; } })();
setOpen(stored != null ? stored === "1" : matchMedia("(min-width: 1100px)").matches, false);
toggle.onclick = () => setOpen(card.hidden, true);
$("live-close").onclick = () => setOpen(false, true);

function setOpen(open: boolean, remember: boolean) {
  card.hidden = !open;
  toggle.setAttribute("aria-expanded", String(open));
  document.body.classList.toggle("live-open", open);
  if (remember) try { localStorage.setItem(KEY, open ? "1" : "0"); } catch {}
  if (open) draw();
}

// --- data --------------------------------------------------------------------
const cm = (v?: number) => (v == null ? "–" : `${Math.round(v)} cm`);
const clock = (t: string) => new Date(t).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });

function show(s: Summary) {
  const r = s.last;
  if (!r) return;
  if (!readings.some((x) => x.t === r.t)) readings.push(r);
  else readings = readings.map((x) => (x.t === r.t ? r : x)); // the official level arrives up to an hour later
  const t0 = Date.now() - DAY_MIN * 60_000;
  readings = readings.filter((x) => Date.parse(x.t) > t0).sort((a, b) => Date.parse(a.t) - Date.parse(b.t));

  if (r.frame_url && photo.src !== r.frame_url) photo.src = r.frame_url;
  card.classList.toggle("blind", !!r.blind);
  $("live-time").textContent = `${clock(r.t)} your time`;
  $("live-sam").textContent = r.blind ? "–" : cm(r.level_cm);

  // USGS publishes its gage 45–60 min after the photo: until then show the
  // latest official value and when it was taken.
  const ref = [...readings].reverse().find((x) => x.ref_cm != null);
  $("live-ref").textContent = cm(ref?.ref_cm);
  $("live-ref-note").textContent = !ref ? "not published yet" : ref.t === r.t ? "same hour" : `at ${clock(ref.t)}`;

  const rise = s.rise_3h_cm_per_min;
  $("live-rise").textContent = rise == null ? "–" : `${rise >= 0 ? "+" : "−"}${Math.abs(rise * 60).toFixed(1)} cm / h`;
  toggle.querySelector(".live-pill-v")!.textContent = r.blind ? "camera blind" : cm(r.level_cm);
  draw();
}

function draw() {
  if (card.hidden || !chart.clientWidth) return;
  const dpr = devicePixelRatio;
  const w = (chart.width = chart.clientWidth * dpr);
  const h = (chart.height = chart.clientHeight * dpr);
  const g = chart.getContext("2d")!;
  g.clearRect(0, 0, w, h);

  const now = Date.now(), span = DAY_MIN * 60_000;
  const values = readings.flatMap((r) => [r.level_cm, r.ref_cm]).filter((v): v is number => v != null);
  if (!values.length) return;
  // At least 1 m of range, so a quiet river looks quiet instead of noisy.
  const mid = (Math.min(...values) + Math.max(...values)) / 2;
  const half = Math.max(50, (Math.max(...values) - Math.min(...values)) / 2 + 10);
  const lo = mid - half, hi = mid + half;
  const X = (t: string) => ((Date.parse(t) - (now - span)) / span) * w;
  const Y = (v: number) => h - ((v - lo) / (hi - lo)) * (h - 18 * dpr) - 4 * dpr;

  const css = getComputedStyle(document.documentElement);
  g.strokeStyle = css.getPropertyValue("--grid");
  g.fillStyle = css.getPropertyValue("--muted");
  g.lineWidth = 1;
  g.font = `${10 * dpr}px ui-monospace, Menlo, monospace`;
  const step = 50;
  for (let v = Math.ceil(lo / step) * step; v <= hi; v += step) {
    g.beginPath(); g.moveTo(0, Y(v)); g.lineTo(w, Y(v)); g.stroke();
    g.fillText(`${v} cm`, 3 * dpr, Y(v) - 3 * dpr);
  }

  // Official gage: a line. SAM 3.1: dots, with a gap where the camera was blind.
  g.strokeStyle = css.getPropertyValue("--truth");
  g.lineWidth = 2 * dpr;
  g.beginPath();
  readings.filter((r) => r.ref_cm != null).forEach((r, i) => (i ? g.lineTo(X(r.t), Y(r.ref_cm!)) : g.moveTo(X(r.t), Y(r.ref_cm!))));
  g.stroke();
  g.fillStyle = "#00beff";
  for (const r of readings) {
    if (r.level_cm == null) continue;
    g.beginPath(); g.arc(X(r.t), Y(r.level_cm), 3 * dpr, 0, Math.PI * 2); g.fill();
  }
}
addEventListener("resize", draw);

fetch(`/api/stations/${ID}/readings?minutes=${DAY_MIN}`)
  .then((r) => (r.ok ? (r.json() as Promise<Reading[]>) : []))
  .then((rs) => {
    readings = rs ?? [];
    return fetch(`/api/stations/${ID}`);
  })
  .then((r) => (r.ok ? (r.json() as Promise<Summary>) : undefined))
  .then((s) => (s ? show(s) : card.classList.add("offline")))
  .catch(() => card.classList.add("offline"));

events.addEventListener("reading", (e) => {
  const s = JSON.parse((e as MessageEvent).data) as Summary;
  if (s.id === ID) show(s);
});
