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
