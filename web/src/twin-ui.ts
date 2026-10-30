import "@fontsource-variable/inter";
import "@fontsource-variable/outfit";

// Presentation only: the three steps at the top, the tuning popover, the
// surge countdown and the look of the camera-node button. The twin's logic (main.ts) owns the
// element ids and their behaviour; this file only watches and decorates them.

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const send = $<HTMLButtonElement>("send");

// main.ts writes "Start camera node" / "Stop camera node (m:ss)" into the button.
const syncNode = () => document.body.classList.toggle("node-on", send.textContent?.startsWith("Stop") ?? false);
new MutationObserver(syncNode).observe(send, { childList: true, characterData: true, subtree: true });

// --- the camera starts by itself -----------------------------------------------
// A visitor (a judge) lands on a stream already being watched: the first mask
// appears within seconds. Only a visible tab starts it, so a tab opened in the
// background spends nothing; main.ts stops it after a few minutes.
let started = false;
function autostart() {
  if (started || document.hidden) return;
  started = true;
  if (!document.body.classList.contains("node-on")) send.click();
}
addEventListener("load", () => setTimeout(autostart, 300));
document.addEventListener("visibilitychange", autostart);

// --- three steps: watch, flood, phone ----------------------------------------------
const steps = $("steps");
const step = (name: string, state: "now" | "done" | "", text?: string) => {
  const li = steps.querySelector<HTMLElement>(`[data-step="${name}"]`)!;
  li.dataset.state = state;
  if (text) li.querySelector(".step-t")!.textContent = text;
};
let flooded = false;
let seen = false; // a frame went out since the camera started: "Sending frame…" in between does not undo it
function syncSteps() {
  const on = document.body.classList.contains("node-on");
  seen = on && (seen || /^Frame sent/.test($("send-state").textContent ?? ""));
  step("watch", on && seen ? "done" : "now", on ? (seen ? "Camera watching" : "Starting the camera…") : "Start the camera");
  if (!document.body.classList.contains("surging")) step("flood", flooded ? "done" : on && seen ? "now" : "", flooded ? "Flash flood sent" : "Trigger a flash flood");
  step("phone", !$("wa-on").hidden ? "done" : flooded ? "now" : "", !$("wa-on").hidden ? "Alerts on your phone" : undefined);
}
for (const id of ["send", "send-state", "wa-on"])
  new MutationObserver(syncSteps).observe($(id), { childList: true, characterData: true, subtree: true, attributes: true });
$("step-watch").addEventListener("click", () => !document.body.classList.contains("node-on") && send.click());
$("step-flood").addEventListener("click", () => !document.body.classList.contains("surging") && $("surge").click());
$("step-phone").addEventListener("click", () => {
  $("wa-card").hidden = false;
  $("wa-card").dispatchEvent(new Event("open"));
});
syncSteps();

// --- tuning popover ----------------------------------------------------------
const tune = $<HTMLButtonElement>("tune");
const tuning = $("tuning");
function setTuning(open: boolean) {
  tuning.hidden = !open;
  tune.setAttribute("aria-expanded", String(open));
}
tune.addEventListener("click", () => setTuning(tuning.hidden));
tuning.querySelector("[data-close]")?.addEventListener("click", () => setTuning(false));
addEventListener("keydown", (e) => e.key === "Escape" && setTuning(false));
addEventListener("pointerdown", (e) => {
  const t = e.target as Node;
  if (!tuning.hidden && !tuning.contains(t) && !tune.contains(t)) setTuning(false);
});

// Tabs: the stream, the scene around it, the camera in the field.
tuning.querySelectorAll<HTMLButtonElement>("[data-tab]").forEach((tab) =>
  tab.addEventListener("click", () => {
    tuning.querySelectorAll("[data-tab]").forEach((t) => t.setAttribute("aria-selected", String(t === tab)));
    tuning.querySelectorAll<HTMLElement>("[data-pane]").forEach((p) => (p.hidden = p.dataset.pane !== tab.dataset.tab));
  }),
);

// People as one-tap choices; main.ts reads the hidden select.
tuning.querySelectorAll<HTMLElement>(".seg-pick").forEach((group) => {
  const select = $<HTMLSelectElement>(group.dataset.for!);
  group.querySelectorAll<HTMLButtonElement>("[data-v]").forEach((b) =>
    b.addEventListener("click", () => {
      group.querySelectorAll("[data-v]").forEach((x) => x.setAttribute("aria-checked", String(x === b)));
      select.value = b.dataset.v!;
      select.dispatchEvent(new Event("input"));
    }),
  );
});

// Sliders without a number say what they mean: "Drizzle", "Gusts", "Night".
tuning.querySelectorAll<HTMLOutputElement>("output[data-words]").forEach((out) => {
  const input = $<HTMLInputElement>(out.htmlFor.value);
  const list = out.dataset.words!.split(",");
  const say = () => (out.textContent = +input.value === 0 ? list[0] : list[Math.min(list.length - 1, 1 + Math.floor(+input.value * (list.length - 1) * 0.999))]);
  input.addEventListener("input", say);
  say();
});

// Filled track on the sliders; main.ts moves their values during a surge.
const sliders = [...tuning.querySelectorAll<HTMLInputElement>('input[type="range"]')];
const painted = new Map<HTMLInputElement, string>();
function paintSliders() {
  for (const s of sliders) {
    if (painted.get(s) === s.value) continue; // touch the style only when the value moved
    painted.set(s, s.value);
    s.style.setProperty("--fill", `${((+s.value - +s.min) / (+s.max - +s.min)) * 100}%`);
  }
}
setInterval(paintSliders, 100);
for (const s of sliders) s.addEventListener("input", paintSliders);
paintSliders();

// --- surge countdown: step 2 counts down to the peak ---------------------------
let surgeEnds = 0;
let surgeTimer = 0;
function stopSurge() {
  clearInterval(surgeTimer);
  document.body.classList.remove("surging");
  syncSteps();
}
$("surge").addEventListener("click", () => {
  // A flood nobody measures shows nothing: start the camera node with it.
  if (!document.body.classList.contains("node-on")) send.click();
  surgeEnds = Date.now() + +$<HTMLSelectElement>("surge-min").value * 60_000;
  flooded = true;
  document.body.classList.add("surging");
  const tick = () => {
    const left = Math.max(0, Math.ceil((surgeEnds - Date.now()) / 1000));
    step("flood", "now", `Flood coming · ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`);
    if (!left) stopSurge();
  };
  clearInterval(surgeTimer);
  tick();
  surgeTimer = window.setInterval(tick, 1000);
  syncSteps();
});
// Moving the level by hand cancels the surge in main.ts, so the countdown goes too.
$("level").addEventListener("input", () => document.body.classList.contains("surging") && stopSurge());
