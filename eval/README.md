# Evaluation

Every number Vigiax publishes comes from a run in this folder, with the command to reproduce it.

## Real flood on a USGS webcam — `results/el-reno-2026-10/`

North Canadian River near El Reno, Oklahoma, 13–16 October 2026: the river rose 3.66 m in about a day.
SAM 3.1 measured the water in 45 hourly frames of the USGS webcam; the level read from the water area is
compared with the official gage height of the same station (USGS 07239500).

```sh
cd server
MODEL_API_KEY=... go run ./cmd/evalusgs -cam OK_North_Canadian_River_near_El_Reno -site 07239500 \
  -from 2026-10-13T00:00:00-05:00 -to 2026-10-16T00:00:00-05:00 -out ../eval/results/el-reno-2026-10
```

Frames and SAM results are cached in `data/raw/usgs/` (not versioned); a rerun with the cache costs nothing.
Imagery and gage data: U.S. Geological Survey, public domain.

## Alerts on the same camera, calm days and the flood — `results/alerts-el-reno-2026-10/`

Every frame from 4 to 30 October 2026 (405 frames, 21 calm days and the flood) went through the live station's
code: SAM 3.1, the rating fitted above, and the alert rules with the station's settings. Each alert is compared
with the time the official gage crossed the same line. With `-agent`, each rule event wakes the agent as in the
service: Muse Spark reads the frame and compares it with a normal-day frame, and Muse Spark Open, served on Baseten,
decides whether to go above the rules.

```sh
cd server
MODEL_API_KEY=... BASETEN_API_KEY=... go run ./cmd/evalalerts -station el-reno \
  -from 2026-10-04T00:00:00-05:00 -to 2026-10-31T00:00:00-05:00 \
  -agent -ref OK_North_Canadian_River_near_El_Reno___2026-10-04T17-00-23Z.jpg \
  -out ../eval/results/alerts-el-reno-2026-10
```

Readings and agent runs are cached in `data/raw/usgs/`; a rerun from the cache costs nothing. The agent's answers
are cached as given, because a new run of a language model can answer differently. This is one flood seen at one
photo an hour: the times are good to about an hour, and the agent's earlier alert is one case, not a rate.

## Digital twin: how early the alert goes out, rules alone and with the agent — `results/twin-2026-10/`

**Simulated.** Three runs of the digital twin through the service as in production (SAM 3.1 on each frame every 5 s,
the rules, Muse Spark on the scene and Muse Spark Open deciding), each a calm start and then a 2-minute flash flood:
the flood alone, with three people on the banks, and as a debris flow. The twin sends its real level with every frame
(`truth_cm`), so each alert is timed against the moment the water really reached its line. The rules alone are
replayed on the same readings with the same code.

```sh
# the service and the twin running locally (server/cmd/vigiax, web/), one twin station per run, then:
cd server
go run ./cmd/evaltwin -data $DATA_DIR/stations -runs plain=twin-evpla1,people=twin-evppl1,debris=twin-evdeb1 \
  -out ../eval/results/twin-2026-10
```

One run per case: the language models can answer differently on another run.

## Scene reading on an AI-generated clip — `results/scene-ai-2026-10/`

**Synthetic.** One frame per second of a 10-second clip generated with Leonardo.ai (a fixed camera over a creek: calm,
then a muddy flood, then logs, branches and rocks), read by Muse Spark and the scene rules. Source and terms in
`data/manifest.json`; cases in `eval/scene/cases-ai.json`.

```sh
cd server
MODEL_API_KEY=... go run ./cmd/evalscene -cases ../eval/scene/cases-ai.json \
  -title "Muse Spark scene reading on an AI-generated clip (synthetic)" -out ../eval/results/scene-ai-2026-10
```
