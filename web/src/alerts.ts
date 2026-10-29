import qrcode from "qrcode-generator";
import "./alerts.css";
import { events } from "./events";

// Alert settings and alerts on screen. The service owns the rules
// (server/internal/alert); this page shows them, lets the visitor change their
// own twin's (and, with a judge's code, the real stations'), and shows each
// alert as it fires.

type Level = "normal" | "attention" | "alert" | "evacuate";
type Rule = {
  enabled: boolean;
  level_cm?: number;
  rise_cm_per_min?: number;
  audience: "leader" | "group" | "everyone";
  channels: string[];
  why: string;
  action: string;
  action_en: string;
};
type Config = {
  place: string;
  place_en: string;
  confirm_readings: number;
  rise_window_minutes: number;
  lower_after_minutes: number;
  rules: Record<Exclude<Level, "normal">, Rule>;
  note?: string;
};
type Event = { t: string; kind: string; from: Level; to: Level; reason: string; level_cm?: number; rise_cm_per_min?: number; by?: string };
type View = {
  station: string;
  config: Config;
  custom: boolean;
  state: { level: Level; since: string; blind: boolean; camera_moved?: boolean };
  events: Event[] | null;
  editable: boolean;
  role: "public" | "judge" | "admin";
  preview: { es: string; en: string };
  subscribers: number;
  whatsapp?: string;
};

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const LEVELS: Exclude<Level, "normal">[] = ["attention", "alert", "evacuate"];
const NAMES: Record<Level, string> = { normal: "Normal", attention: "Attention", alert: "Alert", evacuate: "Evacuate" };
const AUDIENCE = { leader: "Community leader", group: "Village group", everyone: "Everyone" };
const CHANNELS = { whatsapp: "WhatsApp", push: "Dashboard", siren: "Local siren" };

const twin = () => $("station-id").textContent || "twin";
let current = "twin"; // tab: "twin" or a real station id
const stationId = () => (current === "twin" ? twin() : current);
let view: View | null = null;
let lang: "es" | "en" = "en";

const panel = $("alerts");
const toggle = $<HTMLButtonElement>("alerts-toggle");
function setOpen(open: boolean) {
  panel.hidden = !open;
  toggle.setAttribute("aria-expanded", String(open));
  if (open) load();
}
toggle.addEventListener("click", () => setOpen(panel.hidden));
$("alerts-close").addEventListener("click", () => setOpen(false));
addEventListener("keydown", (e) => e.key === "Escape" && !panel.hidden && setOpen(false));

panel.querySelectorAll<HTMLButtonElement>("[data-station]").forEach((b) =>
  b.addEventListener("click", () => {
    current = b.dataset.station!;
    panel.querySelectorAll("[data-station]").forEach((x) => x.setAttribute("aria-selected", String(x === b)));
    load();
  }),
);
panel.querySelectorAll<HTMLButtonElement>("[data-lang]").forEach((b) =>
  b.addEventListener("click", () => {
    lang = b.dataset.lang as "es" | "en";
    panel.querySelectorAll("[data-lang]").forEach((x) => x.setAttribute("aria-selected", String(x === b)));
    if (view) $("preview").textContent = view.preview[lang];
  }),
);

function badge(el: HTMLElement, l: Level) {
  el.dataset.level = l;
  el.textContent = NAMES[l];
}

async function load() {
  const res = await fetch(`/api/stations/${stationId()}/alerts`);
  if (!res.ok) return;
  render(await res.json());
}

function render(v: View) {
  view = v;
  const c = v.config;
  badge($("alerts-now"), v.state.level);
  panel.dataset.level = v.state.level;
  const raised = v.state.level !== "normal" && v.state.since;
  $("alerts-since").textContent = raised ? `since ${clock(v.state.since)}` : v.state.blind ? "camera cannot see" : "";
  const last = (v.events ?? []).filter((e) => e.kind !== "blind").at(-1);
  $("alerts-why").textContent =
    raised && last ? sentence(last.reason)
    : v.state.blind ? "The camera cannot see right now, so the level stays where it was."
    : "The water is below every line. Nobody is being warned.";
  if (current === "twin") badge($("alert-level"), v.state.level);

  const locked = !v.editable;
  panel.classList.toggle("locked", locked);
  $("levels-hint").textContent = locked ? "Tap one to see it" : "Tap one to change it";
  $("alerts-lock").hidden = !locked;
  $("access-form").hidden = !(locked || v.role !== "public") || current === "twin";
  $("access-state").textContent = v.role === "public" ? "" : `Signed in as ${v.role}`;
  $("access-code").hidden = v.role !== "public";
  ($("access-form").querySelector("button") as HTMLButtonElement).textContent = v.role === "public" ? "Unlock" : "Sign out";

  $("levels").replaceChildren(...LEVELS.map((l) => ruleCard(l, c.rules[l], c, locked)));
  const f = $<HTMLFormElement>("alerts-form");
  for (const k of ["place", "confirm_readings", "rise_window_minutes", "lower_after_minutes"] as const) {
    const input = f.elements.namedItem(k) as HTMLInputElement;
    input.value = String(c[k]);
    input.disabled = locked;
  }
  $("alerts-note").textContent = c.note ?? "";
  $<HTMLButtonElement>("alerts-save").disabled = locked;
  $<HTMLButtonElement>("alerts-reset").disabled = locked || !v.custom;
  f.classList.toggle("dirty", v.custom); // Save appears once something changes
  $("preview").textContent = v.preview[lang];

  const wa = $("wa");
  wa.hidden = !v.whatsapp;
  if (v.whatsapp) {
    const code = `ALERTS ${v.station}`;
    const link = `https://wa.me/${v.whatsapp.replace(/\D/g, "")}?text=${encodeURIComponent(code)}`;
    $("wa-code").textContent = code;
    $("wa-number").textContent = v.whatsapp;
    const qr = qrcode(0, "M");
    qr.addData(link);
    qr.make();
    const a = $<HTMLAnchorElement>("wa-qr");
    a.href = link;
    a.innerHTML = qr.createSvgTag({ cellSize: 3, margin: 2, scalable: true });
    $("wa-subs").textContent = v.subscribers ? `${v.subscribers} phone${v.subscribers > 1 ? "s" : ""} signed up now.` : "";
  }

  $("alerts-events").replaceChildren(
    ...(v.events ?? [])
      .slice()
      .reverse()
      .map((e) => {
        const li = document.createElement("li");
        const time = clock(e.t, true);
        li.innerHTML = `<time></time> <span class="level-badge" data-level=""></span> <span class="why"></span>`;
        li.querySelector("time")!.textContent = time;
        badge(li.querySelector(".level-badge")!, e.kind === "blind" ? e.from : e.to);
        li.querySelector(".why")!.textContent =
          e.kind === "false_alarm" ? `Marked as a false alarm (${e.by ?? "someone"})` : e.kind === "blind" ? "Camera cannot see: level kept" : e.kind === "moved" ? "Camera moved: readings paused until it is recalibrated" : sentence(e.reason);
        return li;
      }),
  );
  if (!v.events?.length) $("alerts-events").innerHTML = `<li class="muted">No changes yet. Trigger a flash flood.</li>`;
}

const clock = (t: string, seconds = false) =>
  new Date(t).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit", second: seconds ? "2-digit" : undefined });
const sentence = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) + (s.endsWith(".") ? "" : ".") : "");

function ruleCard(l: Exclude<Level, "normal">, r: Rule, c: Config, locked: boolean): HTMLElement {
  const el = document.createElement("details");
  el.className = "rule";
  el.dataset.level = l;
  el.innerHTML = `
    <summary>
      <span class="level-badge" data-level="${l}">${NAMES[l]}</span>
      <span class="rule-sum"><b></b><span></span></span>
      <label class="switch" title="Active"><input type="checkbox" name="enabled" /><span class="sr-only">${NAMES[l]} active</span></label>
    </summary>
    <fieldset class="rule-body">
      <div class="row">
        <span class="k">Fires when</span>
        <div class="inline">
          <label class="num">level ≥ <input name="level_cm" type="number" min="1" max="5000" step="1" /> cm</label>
          <span class="or">or</span>
          <label class="num">rises ≥ <input name="rise_cm_per_min" type="number" min="0.1" max="500" step="0.1" /> cm/min</label>
        </div>
        <small>The rise is measured over the last ${c.rise_window_minutes} min.</small>
      </div>
      <label class="row"><span class="k">Tells</span>
        <select name="audience">${Object.entries(AUDIENCE).map(([k, v]) => `<option value="${k}">${v}</option>`).join("")}</select>
      </label>
      <div class="row"><span class="k">By</span>
        <div class="chips">${Object.entries(CHANNELS).map(([k, v]) => `<label class="chip"><input type="checkbox" name="ch-${k}" />${v}</label>`).join("")}</div>
      </div>
      <label class="row"><span class="k">Why</span><input name="why" maxlength="200" /></label>
      <label class="row"><span class="k">What to do · English</span><input name="action_en" maxlength="160" /></label>
      <label class="row"><span class="k">What to do · Spanish</span><input name="action" maxlength="160" /></label>
    </fieldset>`;
  const q = <T extends HTMLInputElement | HTMLSelectElement>(n: string) => el.querySelector(`[name="${n}"]`) as T;
  q<HTMLInputElement>("enabled").checked = r.enabled;
  q<HTMLInputElement>("enabled").disabled = locked;
  el.querySelector("fieldset")!.disabled = locked;
  q<HTMLInputElement>("level_cm").value = r.level_cm != null ? String(r.level_cm) : "";
  q<HTMLInputElement>("rise_cm_per_min").value = r.rise_cm_per_min != null ? String(r.rise_cm_per_min) : "";
  q<HTMLSelectElement>("audience").value = r.audience;
  for (const k of Object.keys(CHANNELS)) q<HTMLInputElement>(`ch-${k}`).checked = r.channels.includes(k);
  q<HTMLInputElement>("why").value = r.why;
  q<HTMLInputElement>("action").value = r.action;
  q<HTMLInputElement>("action_en").value = r.action_en;

  // The summary line follows the fields as they change.
  const sum = () => {
    const on = q<HTMLInputElement>("enabled").checked;
    el.classList.toggle("off", !on);
    const lv = q<HTMLInputElement>("level_cm").value, rise = q<HTMLInputElement>("rise_cm_per_min").value;
    const when = [lv && `≥ ${lv} cm`, rise && `rising ${rise} cm/min`].filter(Boolean).join(" or ");
    const by = Object.entries(CHANNELS).filter(([k]) => q<HTMLInputElement>(`ch-${k}`).checked).map(([, v]) => v);
    el.querySelector(".rule-sum b")!.textContent = on ? when || "No trigger set" : "Off";
    el.querySelector(".rule-sum span")!.textContent = on ? `${AUDIENCE[q<HTMLSelectElement>("audience").value as Rule["audience"]]} · ${by.join(", ") || "no channel"}` : "This level never fires";
  };
  el.addEventListener("input", sum);
  el.addEventListener("change", sum);
  sum();
  return el;
}

function collect(): Config {
  const c: Config = structuredClone(view!.config);
  const f = $<HTMLFormElement>("alerts-form");
  const val = (n: string) => (f.elements.namedItem(n) as HTMLInputElement).value;
  c.place = val("place");
  c.confirm_readings = Number(val("confirm_readings"));
  c.rise_window_minutes = Number(val("rise_window_minutes"));
  c.lower_after_minutes = Number(val("lower_after_minutes"));
  for (const l of LEVELS) {
    const el = $("levels").querySelector(`[data-level="${l}"]`)!;
    const q = (n: string) => el.querySelector(`[name="${n}"]`) as HTMLInputElement;
    const num = (n: string) => (q(n).value === "" ? undefined : Number(q(n).value));
    const r = c.rules[l];
    r.enabled = q("enabled").checked;
    r.level_cm = num("level_cm");
    r.rise_cm_per_min = num("rise_cm_per_min");
    r.audience = q("audience").value as Rule["audience"];
    r.channels = Object.keys(CHANNELS).filter((k) => q(`ch-${k}`).checked);
    r.why = q("why").value;
    r.action = q("action").value;
    r.action_en = q("action_en").value;
  }
  return c;
}

for (const ev of ["input", "change"]) $("alerts-form").addEventListener(ev, () => $("alerts-form").classList.add("dirty"));
$("alerts-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const state = $("alerts-state");
  state.textContent = "Saving…";
  const res = await fetch(`/api/stations/${stationId()}/alerts`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(collect()) });
  const body = await res.json();
  if (!res.ok) {
    state.textContent = body.error ?? "Could not save";
    return;
  }
  render(body);
  state.textContent = "Saved";
});
$("alerts-reset").addEventListener("click", async () => {
  const res = await fetch(`/api/stations/${stationId()}/alerts`, { method: "DELETE" });
  if (res.ok) {
    render(await res.json());
    $("alerts-state").textContent = "Defaults restored";
  }
});

$("access-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const signedIn = view && view.role !== "public";
  const input = $<HTMLInputElement>("access-code");
  const res = await fetch("/api/access", {
    method: signedIn ? "DELETE" : "POST",
    headers: { "Content-Type": "application/json" },
    body: signedIn ? undefined : JSON.stringify({ code: input.value }),
  });
  const body = await res.json();
  input.value = "";
  $("access-state").textContent = res.ok ? "" : body.error;
  if (res.ok) load();
});

// --- alerts as they fire ------------------------------------------------------
type AlertMsg = { station: string; event: Event; rule?: Rule; message: { es: string; en: string } };

events.addEventListener("alert", (e) => {
  const a: AlertMsg = JSON.parse((e as MessageEvent).data);
  if (a.station === twin()) {
    badge($("alert-level"), a.event.kind === "blind" ? a.event.from : a.event.to);
    if (!a.rule || a.rule.channels.includes("push")) toast(a);
    if (a.event.kind === "raise" && a.rule?.channels.includes("siren")) siren();
  }
  if (!panel.hidden && a.station === stationId()) load();
});
for (const name of ["alerts_config", "subscribed"]) {
  events.addEventListener(name, (e) => {
    const d = JSON.parse((e as MessageEvent).data);
    if (name === "subscribed" && d.station === twin()) $("wa-on").hidden = false;
    if (!panel.hidden && d.station === stationId()) load();
  });
}

function toast(a: AlertMsg) {
  const el = document.createElement("div");
  el.className = "toast glass";
  el.dataset.level = a.event.kind === "blind" ? a.event.from : a.event.to;
  el.setAttribute("role", "alert");
  el.innerHTML = `<div class="toast-head"><span class="level-badge"></span><span class="toast-where"></span><time></time></div><p class="toast-msg"></p><div class="toast-actions"></div>`;
  badge(el.querySelector(".level-badge")!, el.dataset.level as Level);
  el.querySelector(".toast-where")!.textContent = a.station === twin() ? "Your twin" : a.station === "el-reno" ? "Real river · El Reno" : a.station;
  el.querySelector("time")!.textContent = clock(a.event.t);
  el.querySelector(".toast-msg")!.textContent = a.message.en;
  const actions = el.querySelector(".toast-actions")!;
  const button = (label: string, fn: () => void) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = actions.children.length ? "soft-btn" : "soft-btn primary";
    b.textContent = label;
    b.addEventListener("click", fn);
    actions.append(b);
  };
  if (a.event.kind === "raise") {
    button("Got it", () => el.remove());
    button("False alarm", async () => {
      await fetch(`/api/stations/${a.station}/alerts/false-alarm`, { method: "POST" });
      el.remove();
    });
  } else {
    button("Close", () => el.remove());
  }
  $("toasts").prepend(el);
  while ($("toasts").children.length > 3) $("toasts").lastElementChild!.remove();
}

// A short rising and falling tone, as a local siren would sound.
function siren() {
  try {
    const ctx = new AudioContext();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = "sawtooth";
    gain.gain.value = 0.06;
    osc.connect(gain).connect(ctx.destination);
    const t = ctx.currentTime;
    for (let i = 0; i < 3; i++) {
      osc.frequency.setValueAtTime(600, t + i);
      osc.frequency.linearRampToValueAtTime(1100, t + i + 0.5);
      osc.frequency.linearRampToValueAtTime(600, t + i + 1);
    }
    osc.start();
    osc.stop(t + 3);
    osc.onended = () => ctx.close();
  } catch {
    /* no audio */
  }
}

// The level badge in the top bar, before the panel is ever opened, and
// whether a phone already gets this twin's alerts on WhatsApp.
fetch(`/api/stations/${twin()}/alerts`)
  .then((r) => r.json())
  .then((v: View) => {
    badge($("alert-level"), v.state.level);
    $("wa-on").hidden = !(v.subscribers > 0);
    $("moved").hidden = !v.state.camera_moved;
  })
  .catch(() => {});

// --- a camera knocked off its mount --------------------------------------------
// The service pauses the readings when the gauge is no longer where it was
// calibrated; the feed says so until it is back in place or recalibrated.
const moved = $("moved");
const checkMoved = () =>
  fetch(`/api/stations/${twin()}/alerts`)
    .then((r) => r.json())
    .then((v: View) => (moved.hidden = !v.state.camera_moved))
    .catch(() => {});
events.addEventListener("alert", (e) => {
  const a: AlertMsg = JSON.parse((e as MessageEvent).data);
  if (a.station === twin() && a.event.kind === "moved") moved.hidden = false;
});
for (const name of ["reading", "station"]) {
  events.addEventListener(name, (e) => {
    if (!moved.hidden && JSON.parse((e as MessageEvent).data).id === twin()) checkMoved();
  });
}
$("recalibrate").addEventListener("click", () => $("calibrate").click());
