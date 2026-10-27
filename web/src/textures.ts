import * as THREE from "three";

// Staff gauge face: metres, with the "E" pattern every 10 cm and a number every
// half metre, as on the gauges installed on Colombian rivers. Numbers are
// large so a camera 15 m away can still read them.
export function gaugeTexture(heightM: number): THREE.CanvasTexture {
  const pxPerM = 512;
  const w = 200; // board width = w / pxPerM ≈ 0.39 m
  const h = Math.round(heightM * pxPerM);
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  const g = c.getContext("2d")!;
  g.fillStyle = "#f4f1e6";
  g.fillRect(0, 0, w, h);
  const y = (m: number) => h - m * pxPerM; // metres above the gauge zero → canvas row

  for (let cm = 0; cm < heightM * 100; cm += 10) {
    // Every 10 cm: an "E" made of a spine and three 2 cm bars, alternating sides.
    const red = Math.floor(cm / 100) % 2 === 1;
    g.fillStyle = red ? "#b3261e" : "#1b1b1b";
    const left = (cm / 10) % 2 === 0;
    const x0 = left ? 8 : w / 2;
    const top = y((cm + 10) / 100);
    const bar = 0.02 * pxPerM;
    g.fillRect(left ? x0 : w - 8 - 14, top, 14, 0.1 * pxPerM);
    for (const k of [0, 2, 4]) g.fillRect(x0, top + k * bar, w / 2 - 8, bar);
  }
  g.fillStyle = "#1b1b1b";
  g.font = "bold 92px Arial, Helvetica, sans-serif";
  g.textAlign = "center";
  g.textBaseline = "middle";
  // Each number is centred on the height it names, so the centre of the printed
  // number is the calibration point (the half-metre line shows at both edges).
  for (let m = 0.5; m < heightM; m += 0.5) {
    const cy = y(m);
    g.fillStyle = "#f4f1e6";
    g.fillRect(20, cy - 50, w - 40, 100);
    g.fillStyle = "#1b1b1b";
    g.fillText(m.toFixed(1), w / 2, cy + 4);
    g.fillRect(4, cy - 3, 16, 6);
    g.fillRect(w - 20, cy - 3, 16, 6);
  }
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  return t;
}

// Tileable normal map from a sum of waves, for the water shader.
export function waveNormals(size = 256): THREE.CanvasTexture {
  const waves = Array.from({ length: 14 }, (_, i) => {
    const a = (i * 2.399963) % (2 * Math.PI);
    const k = 1 + (i % 5) * 2; // integer frequencies keep the map tileable
    return { kx: Math.round(Math.cos(a) * k), ky: Math.round(Math.sin(a) * k), amp: 1 / (1 + i * 0.35), ph: i * 1.7 };
  });
  const height = (x: number, y: number) =>
    waves.reduce((s, w) => s + w.amp * Math.sin(2 * Math.PI * (w.kx * x + w.ky * y) + w.ph), 0);
  const c = document.createElement("canvas");
  c.width = c.height = size;
  const g = c.getContext("2d")!;
  const img = g.createImageData(size, size);
  const e = 1 / size;
  for (let j = 0; j < size; j++) {
    for (let i = 0; i < size; i++) {
      const u = i / size;
      const v = j / size;
      const dx = (height(u + e, v) - height(u - e, v)) * 0.06;
      const dy = (height(u, v + e) - height(u, v - e)) * 0.06;
      const n = new THREE.Vector3(-dx, -dy, 1).normalize();
      const p = (j * size + i) * 4;
      img.data[p] = (n.x * 0.5 + 0.5) * 255;
      img.data[p + 1] = (n.y * 0.5 + 0.5) * 255;
      img.data[p + 2] = (n.z * 0.5 + 0.5) * 255;
      img.data[p + 3] = 255;
    }
  }
  g.putImageData(img, 0, 0);
  const t = new THREE.CanvasTexture(c);
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  return t;
}
