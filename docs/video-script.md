# Vigiax — demo video script (draft)

Two pieces, cut from the same recordings:

- **Submission video**: 3:00 at most, English voice-over with burned-in subtitles. The first line says what was
  built during the hackathon (rule from the organizers).
- **Landing loop**: 15–20 s, no sound, for the top of vigiax.bukivo.com.

Rules from the organizers (Discord, 29–31 Oct): at most 3:00 (judges stop there), understandable in English, no
slides. Most of the video is real screen footage of the product, with the live Meta model calls visible. It opens
with one line on what existed before the event and what was built during it. A simulator is fine if it is labeled
as one. A voice presented by a person is preferred over an AI voice.

Every number on screen comes from a run in `eval/` or from the live product (measured on 31 Oct; sources in the
last column). The AI-generated creek clip (Leonardo.ai) is a context shot only, labeled "AI-generated (Leonardo.ai)"
on screen; it never stands in for the product or for a real event. Twin footage is captioned "Simulated". Long runs
are sped up on screen, as the organizers allow.

## Submission video (3:00)


| Time | Picture | Voice-over (and subtitles) | Source |
|---|---|---|---|
| 0:00–0:10 | Title over the SAM view, mask on the water | "Nothing existed before this hackathon: all of Vigiax was built during it. Flash-flood early warning from any camera." | — |
| 0:10–0:25 | AI-generated creek clip, 30-s version sped back up: calm, muddy, then logs and rocks (labeled); source caption for the numbers | "In Colombia, flash floods come down mountain creeks in minutes. Medellín has about 4,200 creeks and fewer than 30 level sensors. When the water rises, someone still goes to look." | SIATA, minuto30.com |
| 0:25–0:39 | The twin opens, captioned "Simulated creek (digital twin)": camera watching, SAM 3.1 mask, yellow waterline at the gauge | "Vigiax turns a camera into a river gauge. Every 5 seconds SAM 3.1, on the Meta Model API, finds the water and reads the level where it meets the gauge." | product |
| 0:39–0:52 | Judge code → Turn on (start-up cut) → masks follow the water; people and logs boxed | "With a GPU, open-weights SAM 3.1 follows the water live: people on the bank, logs coming down." | product |
| 0:52–1:17 | 2-minute flood with three people on the bank (sped up): steps light up; agent console: Muse Spark's reading, Muse Spark Open's decision, "verified", tokens and cost | "A flash flood, with people on the bank. Muse Spark reads the scene; an agent on Muse Spark Open, the open-weights model on Baseten, orders evacuation, and a verifier checks every number it cites. In testing, it warned 23 seconds before the water reached the line." | eval/results/twin-2026-11 |
| 1:17–1:30 | Phone: the WhatsApp alert arrives | "The alert reaches the people downstream on WhatsApp, in plain words, from fixed templates filled with measured values." | product |
| 1:30–1:51 | 2-minute debris flow in the twin (sped up): SAM loses the water in the mud; console: Muse Spark's reading ("thick muddy flow carrying rocks and wooden planks"), scene → evacuate, the agent holds it, verified | "The deadliest case is a debris flow. In thick mud the camera loses the water, and the rules alone never warned. Muse Spark still sees the mud, logs and rocks, and the evacuation goes out a minute before the flow reaches the line." | eval/results/twin-2026-11 (61 s) |
| 1:51–2:18 | El Reno, before and at the peak, with the SAM mask; gage numbers next to SAM's | "Same pipeline on a real river: a USGS webcam in Oklahoma. Through a 3.66-meter flood, SAM 3.1 read the level with a mean error of 6.9 centimeters against the official gage. In 21 calm days it raised no false alarm, and in the flood the agent raised the alert an hour before the rules." | eval/results/el-reno-2026-10, alerts-el-reno-2026-10 |
| 2:18–2:33 | Network selector: 2G / offline / LoRa; bytes sent; local siren | "Mountain villages lose signal first. The node measures on site, sounds a local siren without internet, and sends numbers, not video: ten bytes a reading over LoRa." | product (packet format) |
| 2:33–2:50 | Agent console and the day's spend in the header (real product, no card) | "The cheap loop runs always; the language models wake only when something changes. Watching the real river for 27 days, one photo an hour, cost one dollar thirty-five in model calls." | alerts-el-reno-2026-10: SAM $1.28 + agent $0.07 |
| 2:50–3:00 | URL and QR to the live twin | "Any camera. Any creek. Before the water. Try it live at vigiax.bukivo.com." | — |

Numbers to keep straight: the agent results are one run per case (twin) and one flood (El Reno), and the README says
so. A station sending a frame every 5 s costs far more than the hourly river (SAM: about $3.60 a day at one frame a
minute); never quote the river's cost for the twin's pace.

## Landing loop (15–20 s, no sound)

1. SAM view, calm creek, mask on the water (2 s).
2. "Flash flood" pressed; water rises, the waterline follows, steps light up (8 s).
3. The badge climbs to Evacuate; the WhatsApp alert lands on a phone (4 s).
4. El Reno at the peak with the mask, "Real river · mean error 6.9 cm against the official gage" (4 s).

## Shot list (record on vigiax.bukivo.com)

| # | What | How |
|---|---|---|
| 1 | Twin opening in the SAM view | Chrome at 1920×1080, fresh station (`?station=twin-xxxxxx` with a new id), screen recording |
| 2 | Judge code, GPU on, live masks with people and logs | Judge code, people 3, logs on; wait for "Live GPU on"; record 30 s |
| 3 | Flood with people on the bank | Phone signed up first (shot 5). Flood length 2 min, people 3 set after the camera's first minute, then "Flash flood"; record until evacuate and the agent's run |
| 4 | Agent console during the flood | Alerts panel open after the raise: scene, decision, verified, cost |
| 5 | WhatsApp alert on the phone | Phone signed up with the QR before shot 3; screen-record the phone |
| 6 | Debris flow | New station; after the first minute tick "Debris flow" and "Logs", then "Flash flood" (2 min); record until evacuate, with the console |
| 7 | El Reno before / peak | The landing's Measure band (slider between the photo and SAM's mask) |
| 8 | Low connectivity | Selector, offline, siren, bytes |
| 9 | Context | `data/raw/ai/creek-debris-flow-hailuo03-x3.mp4` (AI-generated, Leonardo.ai; labeled) |

Turn the GPU off after recording (`Turn off`, or it stops 3 minutes after the tab closes).
