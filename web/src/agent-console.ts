import { events } from "./events";
import { STATION } from "./station";

// The agent's console in the alert settings: each run as it happens (server
// events "agent") and the last runs of the station on screen.

type Step = { t: string; kind: string; text: string; usd?: number };
type Run = { steps: Step[] | null; verified: boolean; escalated: boolean; cost_usd: number };

const log = document.getElementById("agent-log")!;
const shown = () => {
  const tab = document.querySelector<HTMLElement>("#alerts [data-station][aria-selected='true']")?.dataset.station ?? "twin";
  return tab === "twin" ? STATION : tab;
};

function line(s: Step): HTMLLIElement {
  const li = document.createElement("li");
  li.dataset.kind = s.kind;
  const time = document.createElement("time");
  time.textContent = new Date(s.t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  const kind = document.createElement("span");
  kind.className = "kind";
  kind.textContent = s.kind;
  const text = document.createElement("span");
  text.className = "text";
  text.textContent = s.text + (s.usd ? ` · $${s.usd.toFixed(4)}` : "");
  li.append(time, kind, text);
  return li;
}

async function load() {
  const res = await fetch(`/api/stations/${shown()}/agent`);
  if (!res.ok) {
    log.innerHTML = `<li class="muted">The agent has not run for this station yet. Trigger a flash flood.</li>`;
    return;
  }
  const body: { enabled: boolean; runs: Run[] | null } = await res.json();
  const runs = (body.runs ?? []).slice(-3);
  log.replaceChildren(...runs.flatMap((r) => (r.steps ?? []).map(line)));
  if (!body.enabled) log.innerHTML = `<li class="muted">The agent is off on this server.</li>`;
  else if (!runs.length) log.innerHTML = `<li class="muted">The agent has not run for this station yet. Trigger a flash flood.</li>`;
  log.scrollTop = log.scrollHeight;
}

events.addEventListener("agent", (e) => {
  const d: { station: string; step: Step } = JSON.parse((e as MessageEvent).data);
  if (d.station !== shown()) return;
  log.querySelector(".muted")?.remove();
  log.append(line(d.step));
  while (log.children.length > 40) log.firstElementChild!.remove();
  log.scrollTop = log.scrollHeight;
});

document.getElementById("alerts-toggle")?.addEventListener("click", () => setTimeout(load, 50));
document.querySelectorAll("#alerts [data-station]").forEach((b) => b.addEventListener("click", () => setTimeout(load, 50)));
