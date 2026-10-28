import { events } from "./events";
import { STATION } from "./station";

// The agent's console in the alerts panel: one card per run, newest first.
// A run reads as a short story: what woke the agent, what Muse Spark saw,
// what Muse Spark Open decided, and what the verifier made of each answer.
// Live runs arrive step by step (server events "agent"); finished ones come
// from GET /api/stations/<id>/agent.

type Step = { t: string; kind: string; text: string; usd?: number };
type Woke = { t: string; kind: string; from: string; to: string; reason: string };
type Run = {
  steps: Step[] | null;
  facts: { now: string; woke_by: Woke; scene?: { summary: string } };
  decision?: { level: string; action: string };
  verified: boolean;
  escalated: boolean;
  attempts: number;
  tokens: { prompt_tokens: number; completion_tokens: number };
  cost_usd: number;
  seconds: number;
};

const NAMES: Record<string, string> = { normal: "Normal", attention: "Attention", alert: "Alert", evacuate: "Evacuate" };
const log = document.getElementById("agent-log")!;
const shown = () => {
  const tab = document.querySelector<HTMLElement>("#alerts [data-station][aria-selected='true']")?.dataset.station ?? "twin";
  return tab === "twin" ? STATION : tab;
};
const clock = (t: string) => new Date(t).toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit", second: "2-digit" });
const usd = (v: number) => `$${v.toFixed(v < 0.01 ? 4 : 3)}`;
const name = (l: string) => NAMES[l] ?? l;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text = ""): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  e.textContent = text;
  return e;
}

// One line of a run. "think" carries "raise → alert: reason".
function stepLine(s: Step, rejected = false): HTMLLIElement {
  const li = el("li", "step");
  li.dataset.kind = s.kind;
  let label = "", body = s.text, note = "";
  switch (s.kind) {
    case "wake": {
      label = "Woke up";
      const m = s.text.replace(/^woken by /, "").match(/^(\w+) \((\w+) → (\w+)\): ([\s\S]*)$/);
      if (m) {
        const why = m[4] ? `${m[4][0].toUpperCase()}${m[4].slice(1)}.` : "";
        body =
          m[1] === "jump" ? `The reading jumped. ${why}`
          : m[1] === "blind" ? `The camera cannot see. ${why}`
          : `The rules moved ${name(m[2])} → ${name(m[3])}. ${why}`;
      }
      break;
    }
    case "scene":
      label = "Muse Spark looked";
      body = s.text.replace(/^Muse Spark: /, "");
      break;
    case "think": {
      label = "Muse Spark Open";
      const m = s.text.match(/^(hold|raise) → (\w+): ([\s\S]*)$/);
      if (m) {
        note = m[1] === "raise" ? `Raise to ${name(m[2])}` : `Hold ${name(m[2])}`;
        body = m[3];
      }
      break;
    }
    case "verify":
      label = s.text.startsWith("rejected") || s.text.includes("asking again") ? "Verifier rejected" : "Verifier passed";
      li.dataset.ok = String(label === "Verifier passed");
      body = s.text.replace(/^rejected: /, "").replace("which is not in the facts", "which is not in the measurements");
      if (body === "numbers match the measurements") body = "Every number it cites is in the measurements.";
      else body = `${body[0].toUpperCase()}${body.slice(1)}${body.endsWith(".") ? "" : "."}`;
      break;
    case "decide":
      label = "Decided";
      break;
    case "escalate":
      label = "A person decides";
      break;
    case "error":
      label = "Error";
      break;
  }
  if (rejected) li.classList.add("rejected");
  const head = el("p", "step-head");
  head.append(el("span", "step-label", label));
  if (note) head.append(el("span", "step-note", note));
  if (s.usd) head.append(el("span", "step-usd", usd(s.usd)));
  li.append(head, el("p", "step-body", body));
  return li;
}

function verdict(r: Pick<Run, "decision" | "verified" | "escalated">): { text: string; level: string } {
  if (r.escalated || !r.decision) return { text: "A person decides", level: "escalate" };
  const l = r.decision.level;
  return { text: r.decision.action === "raise" ? `Raised to ${name(l)}` : `Holds ${name(l)}`, level: l };
}

function card(): { root: HTMLElement; head: HTMLElement; steps: HTMLOListElement } {
  const root = el("article", "run");
  const head = el("header", "run-head");
  const steps = el("ol", "run-steps");
  root.append(head, steps);
  return { root, head, steps };
}

function fillHead(head: HTMLElement, v: { text: string; level: string }, meta: string[], t: string) {
  const badge = el("span", "level-badge", v.text);
  badge.dataset.level = v.level;
  head.replaceChildren(badge, el("span", "run-meta", meta.join(" · ")), el("time", "run-time", clock(t)));
}

function runCard(r: Run): HTMLElement {
  const c = card();
  const meta = [
    r.verified ? "verified" : "not verified",
    r.attempts > 1 ? `${r.attempts} answers` : "",
    `${r.seconds.toFixed(1)} s`,
    usd(r.cost_usd),
  ].filter(Boolean);
  fillHead(c.head, verdict(r), meta, r.facts.now);
  const w = r.facts.woke_by;
  c.steps.append(stepLine({ t: w.t, kind: "wake", text: `${w.kind} (${w.from} → ${w.to}): ${w.reason}` }));
  if (r.facts.scene?.summary) c.steps.append(stepLine({ t: w.t, kind: "scene", text: r.facts.scene.summary }));
  // An answer the verifier rejected is shown struck through, followed by why.
  const steps = r.steps ?? [];
  steps.forEach((s, i) => {
    const next = steps[i + 1];
    const rejected = s.kind === "think" && next?.kind === "verify" && next.text.startsWith("rejected");
    c.steps.append(stepLine(s, rejected));
  });
  if (r.escalated) c.steps.append(stepLine({ t: r.facts.now, kind: "escalate", text: "No verified answer: the rule level stands." }));
  return c.root;
}

function empty(text: string) {
  log.replaceChildren(el("p", "agent-empty", text));
}

async function load() {
  const res = await fetch(`/api/stations/${shown()}/agent`);
  if (!res.ok) return empty("The agent has not run for this station yet. Trigger a flash flood.");
  const body: { enabled: boolean; runs: Run[] | null } = await res.json();
  if (!body.enabled) return empty("The agent is off on this server. The rules still raise every alert.");
  const runs = (body.runs ?? []).slice(-3).reverse();
  if (!runs.length) return empty("The agent has not run for this station yet. Trigger a flash flood.");
  live = null;
  log.replaceChildren(...runs.map(runCard));
}

// A run in progress: its card fills in as the steps arrive.
let live: ReturnType<typeof card> | null = null;
let lastThink: HTMLLIElement | null = null;
events.addEventListener("agent", (e) => {
  const d: { station: string; step: Step } = JSON.parse((e as MessageEvent).data);
  if (d.station !== shown()) return;
  const s = d.step;
  if (!live || s.kind === "wake") {
    log.querySelector(".agent-empty")?.remove();
    live = card();
    live.root.classList.add("running");
    fillHead(live.head, { text: "Thinking…", level: "thinking" }, [], s.t);
    log.prepend(live.root);
    while (log.children.length > 3) log.lastElementChild!.remove();
  }
  if (s.kind === "verify" && s.text.startsWith("rejected")) lastThink?.classList.add("rejected");
  const line = stepLine(s);
  if (s.kind === "think") lastThink = line;
  if (s.kind !== "decide") live.steps.append(line);
  if (s.kind === "decide" || (s.kind === "error" && s.text.includes("budget"))) {
    live = null;
    setTimeout(load, 400); // the finished run, with its totals
  }
});

document.getElementById("alerts-toggle")?.addEventListener("click", () => setTimeout(load, 50));
document.querySelectorAll("#alerts [data-station]").forEach((b) => b.addEventListener("click", () => setTimeout(load, 50)));
