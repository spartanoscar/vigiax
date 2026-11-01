# Assets

Everything the digital twin draws is made in code during the event (terrain, water, trees, rocks, debris, the
gauge face, mist), except the models and sounds below.

| File | What | Source | Licence |
|---|---|---|---|
| `web/public/models/woman.glb` | Woman in a red rain jacket, walking with her phone | Generated with [Meshy](https://www.meshy.ai) for Vigiax on 1 Nov 2026 (text to 3D, auto-rig, "Walking with Phone" animation) | Meshy paid plan: the creator owns the generated asset |
| `web/public/models/farmer.glb` | Farmer in a yellow poncho and straw hat, walking slowly | Same, "Walk Slowly and Look Around" animation | Same |
| `web/public/models/boy.glb` | Teenage boy in a blue hoodie, walking | Same, "Confident Strut" animation | Same |
| `web/public/models/footbridge.glb` | Rustic wooden footbridge | Generated with Meshy for Vigiax on 1 Nov 2026 (text to 3D) | Same |
| `web/public/audio/stream-calm.mp3` | A calm mountain stream over rocks (22 s loop) | Generated with [ElevenLabs](https://elevenlabs.io) sound effects for Vigiax on 1 Nov 2026 (text to sound) | ElevenLabs paid plan: commercial use, no attribution required |
| `web/public/audio/stream-flood.mp3` | A muddy flash flood rushing down a creek (22 s loop) | Same | Same |

The models were reduced for the web with [glTF Transform](https://gltf-transform.dev) (textures to 1024 px WebP,
animations resampled, geometry quantized).

Footage and photos (USGS webcams, the AI-generated creek clip) are listed with their source and licence in
`data/manifest.json`.
