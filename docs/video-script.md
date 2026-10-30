# Vigiax — demo video script (draft)

Two pieces, cut from the same recordings:

- **Submission video**: 3:00 at most, English voice-over with burned-in subtitles. The first line says what was
  built during the hackathon (rule from the organizers).
- **Landing loop**: 15–20 s, no sound, for the top of vigiax.bukivo.com.

Every number on screen comes from a run in `eval/` or from the live product. The ones marked **[eval]** are
measured on Saturday; until then they stay out of the cut. Any AI-generated clip (context shots only: a mountain
village, rain, a swollen creek) is labeled "AI-generated" on screen. It never stands in for the product or for a
real event.

## Submission video (3:00)

| Time | Picture | Voice-over (and subtitles) |
|---|---|---|
| 0:00–0:08 | Title card over the SAM view, mask on the water | "Built during the Meta Global AI Developer Hackathon: Vigiax, flash-flood early warning from any camera." |
| 0:08–0:30 | Context shots (labeled if AI-generated); Medellín creek map card with source | "In Colombia, flash floods come down mountain creeks in minutes. Medellín has about 4,200 creeks and fewer than 30 level sensors. When the water rises, someone still goes to look." (source on screen: SIATA, minuto30.com) |
| 0:30–0:45 | The twin opens: camera watching, SAM 3.1 mask, yellow waterline at the gauge | "Vigiax turns a camera into a river gauge. Every 5 seconds SAM 3.1 finds the water and the service reads the level where it meets the gauge." |
| 0:45–1:05 | Judge code → Turn on → masks move with the water; people and logs boxed | "With a GPU, open-weights SAM 3.1 follows the water live: people on the bank, logs coming down." |
| 1:05–1:35 | Flash flood (1 min): water rises, steps light up, attention → alert → evacuate within a minute; agent console explains | "A flash flood. The rules raise the alert as the water crosses each line. When the level changes, an agent on Muse Spark Open looks at the scene and explains why; a verifier checks every number it cites." |
| 1:35–1:50 | Phone: the WhatsApp alert arrives | "The alert reaches the people downstream on WhatsApp, in plain words, from fixed templates filled with measured values." |
| 1:50–2:15 | El Reno, before and at the peak, with the SAM mask; gage numbers next to SAM's | "Same pipeline on a real river: a USGS webcam in Oklahoma. Through a 3.66-meter flood, SAM 3.1 read the level with a mean error of 6.9 centimeters against the official gage, on frames it never saw while calibrating." |
| 2:15–2:35 | Network selector: 2G / offline / LoRa; bytes sent; local siren | "Mountain villages lose signal first. The node measures on site, sounds a local siren without internet, and sends numbers, not video: **[bytes per reading]** bytes per reading." |
| 2:35–2:50 | Card: the Meta stack and what it costs | "SAM 3.1 measures, Muse Spark reads the scene, Muse Spark Open decides. The cheap loop runs always; the language models wake only when something changes: **[eval: cost per station per day]**." |
| 2:50–3:00 | URL and QR to the live twin | "Any camera. Any creek. Minutes before the water. Try it live at vigiax.bukivo.com." |

Lines to add after Saturday, if the numbers hold: lead time against the rules alone **[eval: minutes]** and false
alarms on calm days **[eval: count over N days]**.

## Landing loop (15–20 s, no sound)

1. SAM view, calm creek, mask on the water (2 s).
2. "Flash flood" pressed; water rises, the waterline follows, steps light up (8 s).
3. The badge climbs to Evacuate; the WhatsApp alert lands on a phone (4 s).
4. El Reno at the peak with the mask, "Real river · mean error 6.9 cm against the official gage" (4 s).

## Shot list (record Saturday, after the evaluation)

| # | What | How |
|---|---|---|
| 1 | Twin opening in the SAM view | Chrome at 1920×1080, fresh station (`?station=twin-xxxxxx` with a new id), screen recording |
| 2 | Judge code, GPU on, live masks with people and logs | Judge code, people 3, logs on; wait for "Live GPU on"; record 30 s |
| 3 | 1-minute flash flood from the steps | Same session; record the full minute |
| 4 | Agent console during the flood | Alerts panel open after the raise |
| 5 | WhatsApp alert on the phone | Phone signed up with the QR before shot 3; screen-record the phone |
| 6 | El Reno before / peak | `web/public/img/el-reno-before.webp`, `el-reno-peak.webp`, with captions |
| 7 | Low connectivity | After Friday's feature: selector, offline, siren, bytes |
| 8 | Context shots | Own footage, licensed clips (in `data/manifest.json`) or AI-generated (labeled) |

Turn the GPU off after recording (`Turn off`, or it stops 3 minutes after the tab closes).
