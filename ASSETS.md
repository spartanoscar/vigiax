# Assets

Everything the digital twin draws is made in code during the event (terrain, water, trees, rocks, debris, the
gauge face, mist), except the models below.

| File | What | Source | Licence |
|---|---|---|---|
| `web/public/models/woman.glb` | Woman in a red rain jacket, walking with her phone | Generated with [Meshy](https://www.meshy.ai) for Vigiax on 1 Nov 2026 (text to 3D, auto-rig, "Walking with Phone" animation) | Meshy paid plan: the creator owns the generated asset |
| `web/public/models/farmer.glb` | Farmer in a yellow poncho and straw hat, walking slowly | Same, "Walk Slowly and Look Around" animation | Same |
| `web/public/models/boy.glb` | Teenage boy in a blue hoodie, walking | Same, "Confident Strut" animation | Same |
| `web/public/models/footbridge.glb` | Rustic wooden footbridge | Generated with Meshy for Vigiax on 1 Nov 2026 (text to 3D) | Same |

The models were reduced for the web with [glTF Transform](https://gltf-transform.dev) (textures to 1024 px WebP,
animations resampled, geometry quantized).

Footage and photos (USGS webcams, the AI-generated creek clip) are listed with their source and licence in
`data/manifest.json`.
