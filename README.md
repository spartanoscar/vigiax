# Vigiax

**Any camera becomes a river gauge and a flash-flood early-warning system.**

> Built during the [Meta Global AI Developer Hackathon](https://cerebralvalley.ai/e/meta-ai-developer-hackathon)
> (Indie track, theme *Build for Your World*). Everything in this repository was written during the event,
> starting October 26, 2026. **Status: work in progress** — this README describes the plan and will be
> updated with screenshots, measured results and the live demo as each part ships.

## The problem

In Colombia, flash floods (*crecientes súbitas*) kill people every rainy season. They rise in minutes on
small mountain streams, and almost none of those streams has a sensor. Official bulletins work at the
river-basin or municipality level, not stream by stream, and where a level sensor exists, an alert is often
still confirmed by someone looking at the water.

Satellites see the flood after it happened. Vigiax watches the stream rising, minutes before it arrives.

## How it works

```
camera node ──frame every few s──▶ SAM 3.1 ──water mask──▶ level (cm) + rate of rise
                                                                  │
                                         something changed? ──────┤
                                                                  ▼
                                   Muse Spark reads the scene: muddy water, logs, people near the bank
                                                                  │
                                                                  ▼
                     long-running agent (Muse Spark Open): memory over hours, raise / hold / lower alert
                                                                  │
                                                                  ▼
                                    WhatsApp + web push: short, plain Spanish, what to do now
```

1. **Camera node** — a fixed camera, a phone browser, or a virtual camera inside the digital twin sends a
   frame every few seconds.
2. **Measurement (always on, cheap)** — SAM 3.1 segments the water against a reference marker; the server turns
   the mask into a water level and a rate of rise.
3. **Scene reading (on events)** — when the level or the image changes, Muse Spark describes what matters:
   turbid or muddy water, logs and debris, people or vehicles near the bank.
4. **Agent (on events)** — a long-running agent on Muse Spark Open keeps the trend, the alerts already sent and
   who acknowledged them, and decides the alert level. The language model never runs on the hot path, so a
   node can be watched 24/7 on a small budget.
5. **Alerts that cannot lie** — alert texts come from fixed templates filled with measured values; the agent
   chooses the level and explains why, and a check validates every number it cites against the measurements.
6. **Digital twin** — a 3D stream in the browser with controls for rain, water level, mud, logs, people,
   day/night and network quality. Anyone can trigger a surge and receive the alert on their phone. Openly
   licensed real footage shows the same pipeline working outside the simulation.

## Built for low connectivity

- The node measures locally and can sound a local siren with no internet.
- It sends numbers (bytes), not video.
- Store-and-forward when the link drops; LoRa / SMS as fallback; an upstream node warns the village downstream.
- The twin has a network selector (normal / 2G / offline / LoRa) to show each case.

## Meta models

| Model | Role |
|---|---|
| SAM 3.1 | Water segmentation → level and rate of rise |
| Muse Spark | Scene reading on events (mud, debris, people at risk) |
| Muse Spark Open | Long-running alert agent with memory |

## Repository layout (planned)

```
web/      digital twin + dashboard (Vite, TypeScript, Three.js)
server/   Go service: frame ingest, Meta API calls, agent loop, alerts, SSE to the dashboard
eval/     evaluation on licensed real footage: warning lead time, false alarms
data/     manifest.json with source, license and author of every clip
docs/     architecture, demo script
```

Every number published here (lead time, accuracy, latency, cost per hour) will come from a run in `eval/`,
with the command to reproduce it.

## License

[MIT](LICENSE). Third-party footage and 3D assets keep their own licenses, listed in `data/manifest.json`
and `ASSETS.md`.
