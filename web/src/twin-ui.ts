import "@fontsource-variable/inter";
import "@fontsource-variable/outfit";

// Presentation only: the intro card, the tuning popover, the surge countdown
// and the look of the camera-node button. The twin's logic (main.ts) owns the
// element ids and their behaviour; this file only watches and decorates them.

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const send = $<HTMLButtonElement>("send");

// main.ts writes "Start camera node" / "Stop camera node (m:ss)" into the button.
const syncNode = () => document.body.classList.toggle("node-on", send.textContent?.startsWith("Stop") ?? false);
new MutationObserver(syncNode).observe(send, { childList: true, characterData: true, subtree: true });

// --- intro, once per browser session ---------------------------------------
const intro = $("intro");
const seen = (() => {
  try {
    return sessionStorage.getItem("vigiax-intro") === "1";
  } catch {
    return false;
  }
})();
function closeIntro() {
  intro.classList.add("out");
  setTimeout(() => (intro.hidden = true), 400);
  try {
    sessionStorage.setItem("vigiax-intro", "1");
  } catch {
    /* private mode */
  }
}
if (!seen) {
  intro.hidden = false;
  intro.querySelector<HTMLButtonElement>("[data-start]")?.focus();
}
intro.querySelector("[data-start]")?.addEventListener("click", () => {
  if (!document.body.classList.contains("node-on")) send.click();
  closeIntro();
});
intro.querySelector("[data-close]")?.addEventListener("click", closeIntro);

// --- tuning popover ----------------------------------------------------------
const tune = $<HTMLButtonElement>("tune");
const tuning = $("tuning");
function setTuning(open: boolean) {
  tuning.hidden = !open;
  tune.setAttribute("aria-expanded", String(open));
}
tune.addEventListener("click", () => setTuning(tuning.hidden));
tuning.querySelector("[data-close]")?.addEventListener("click", () => setTuning(false));
addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (!intro.hidden) closeIntro();
  else setTuning(false);
});
addEventListener("pointerdown", (e) => {
  const t = e.target as Node;
  if (!tuning.hidden && !tuning.contains(t) && !tune.contains(t)) setTuning(false);
});

// Filled track on the sliders; main.ts moves their values during a surge.
const sliders = [$<HTMLInputElement>("level"), $<HTMLInputElement>("mud")];
function paintSliders() {
  for (const s of sliders) s.style.setProperty("--fill", `${((+s.value - +s.min) / (+s.max - +s.min)) * 100}%`);
  requestAnimationFrame(paintSliders);
}
paintSliders();

// --- surge countdown ---------------------------------------------------------
const banner = $("surge-banner");
const count = $("surge-count");
let surgeEnds = 0;
let surgeTimer = 0;
function stopSurge() {
  clearInterval(surgeTimer);
  banner.hidden = true;
  document.body.classList.remove("surging");
}
$("surge").addEventListener("click", () => {
  // A flood nobody measures shows nothing: start the camera node with it.
  if (!document.body.classList.contains("node-on")) send.click();
  surgeEnds = Date.now() + +$<HTMLSelectElement>("surge-min").value * 60_000;
  banner.hidden = false;
  document.body.classList.add("surging");
  const tick = () => {
    const left = Math.max(0, Math.ceil((surgeEnds - Date.now()) / 1000));
    count.textContent = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
    if (!left) stopSurge();
  };
  clearInterval(surgeTimer);
  tick();
  surgeTimer = window.setInterval(tick, 1000);
});
// Moving the level by hand cancels the surge in main.ts, so the banner goes too.
sliders[0].addEventListener("input", stopSurge);
