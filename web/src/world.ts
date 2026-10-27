import * as THREE from "three";
import { Sky } from "three/addons/objects/Sky.js";
import { gaugeTexture, waveNormals } from "./textures";

// A mountain stream in a small valley, units in metres. The stream runs along
// -z (away from the camera node); y = 0 is the gauge zero at the stream bed.

export const GAUGE_HEIGHT = 4; // m of printed gauge above the bed
export const GAUGE_WIDTH = 200 / 512; // m, from the texture (200 px at 512 px/m)
export const GAUGE_POS = new THREE.Vector3(0, 0, -6); // x is set from the channel below
const BED_HALF_WIDTH = 3.2;

// Channel centre line: a gentle meander.
const centre = (z: number) => 2.2 * Math.sin(z * 0.045) + 0.8 * Math.sin(z * 0.11 + 1);

function hash(x: number, z: number) {
  const s = Math.sin(x * 127.1 + z * 311.7) * 43758.5453;
  return s - Math.floor(s);
}
function noise(x: number, z: number) {
  const xi = Math.floor(x), zi = Math.floor(z);
  const xf = x - xi, zf = z - zi;
  const u = xf * xf * (3 - 2 * xf), v = zf * zf * (3 - 2 * zf);
  const a = hash(xi, zi), b = hash(xi + 1, zi), c = hash(xi, zi + 1), d = hash(xi + 1, zi + 1);
  return a + (b - a) * u + (c - a) * v + (a - b - c + d) * u * v;
}
const fbm = (x: number, z: number) => noise(x, z) * 0.5 + noise(x * 2.1, z * 2.1) * 0.25 + noise(x * 4.3, z * 4.3) * 0.125;

export function groundHeight(x: number, z: number): number {
  const d = Math.abs(x - centre(z));
  const bed = -0.15 + fbm(x * 0.8, z * 0.8) * 0.3;
  if (d < BED_HALF_WIDTH) return bed;
  const bank = THREE.MathUtils.smoothstep(d, BED_HALF_WIDTH, BED_HALF_WIDTH + 5) * 4.2;
  const hills = Math.max(0, d - BED_HALF_WIDTH - 5) * 0.45 + fbm(x * 0.12, z * 0.12) * 6 * THREE.MathUtils.smoothstep(d, 8, 20);
  return bed + bank + hills + fbm(x * 0.5, z * 0.5) * 0.6 * THREE.MathUtils.smoothstep(d, BED_HALF_WIDTH, 6);
}

export interface WorldState {
  levelM: number; // water surface above gauge zero
  mud: number; // 0 clear .. 1 thick brown
}

export class World {
  readonly scene = new THREE.Scene();
  readonly gauge: THREE.Group;
  private water: THREE.Mesh<THREE.PlaneGeometry, THREE.MeshStandardMaterial>;
  private waves = waveNormals();
  private sky: Sky;
  private sunDir = new THREE.Vector3();
  private sun = new THREE.DirectionalLight(0xfff1dd, 2.6);
  private clearColor = new THREE.Color(0x3e5a4c); // Andean stream: green-grey, not sky blue
  private mudColor = new THREE.Color(0x8a6744);

  constructor() {
    GAUGE_POS.x = centre(GAUGE_POS.z) + BED_HALF_WIDTH - 0.6;

    this.sky = new Sky();
    this.sky.scale.setScalar(4000);
    const u = this.sky.material.uniforms;
    u.turbidity.value = 6;
    u.rayleigh.value = 1.6;
    u.mieCoefficient.value = 0.004;
    u.mieDirectionalG.value = 0.85;
    const sunDir = this.sunDir.setFromSphericalCoords(1, THREE.MathUtils.degToRad(58), THREE.MathUtils.degToRad(150));
    u.sunPosition.value.copy(sunDir);
    this.scene.add(this.sky);

    this.sun.position.copy(sunDir).multiplyScalar(80);
    this.sun.castShadow = true;
    this.sun.shadow.mapSize.set(2048, 2048);
    Object.assign(this.sun.shadow.camera, { left: -40, right: 40, top: 40, bottom: -40, far: 200 });
    this.scene.add(this.sun, new THREE.HemisphereLight(0xcfe3ff, 0x4a5a2a, 1.1));
    this.scene.fog = new THREE.Fog(0xb9cdd6, 60, 180);

    this.scene.add(this.terrain());
    this.scene.add(this.rocks());
    this.scene.add(this.trees());

    // An opaque, slightly glossy surface with moving ripples. A mirror (three's
    // Water shader) reflected the sky so well that SAM 3.1 found no water in it.
    this.waves.repeat.set(6, 40);
    this.water = new THREE.Mesh(
      new THREE.PlaneGeometry(40, 260),
      new THREE.MeshStandardMaterial({
        color: this.clearColor.clone(),
        roughness: 0.12,
        metalness: 0,
        envMapIntensity: 0.85,
        normalMap: this.waves,
        normalScale: new THREE.Vector2(0.8, 0.8),
      }),
    );
    this.water.rotation.x = -Math.PI / 2;
    this.water.position.set(0, 0.35, -60);
    this.water.receiveShadow = true;
    this.scene.add(this.water);

    this.gauge = this.makeGauge();
    this.scene.add(this.gauge);
  }

  private terrain(): THREE.Mesh {
    const geo = new THREE.PlaneGeometry(140, 260, 280, 520);
    geo.rotateX(-Math.PI / 2);
    geo.translate(0, 0, -60);
    const pos = geo.attributes.position as THREE.BufferAttribute;
    const colors = new Float32Array(pos.count * 3);
    const gravel = new THREE.Color(0x6b6156), mud = new THREE.Color(0x6e5338), grass = new THREE.Color(0x4f7a2f), dark = new THREE.Color(0x2f4f22);
    const col = new THREE.Color();
    for (let i = 0; i < pos.count; i++) {
      const x = pos.getX(i), z = pos.getZ(i);
      const h = groundHeight(x, z);
      pos.setY(i, h);
      const d = Math.abs(x - centre(z));
      if (d < BED_HALF_WIDTH) col.copy(gravel).lerp(mud, fbm(x, z));
      else if (h < 2.2) col.copy(mud).lerp(grass, THREE.MathUtils.smoothstep(h, 0.8, 2.2));
      else col.copy(grass).lerp(dark, fbm(x * 0.3, z * 0.3));
      col.offsetHSL(0, 0, (hash(x, z) - 0.5) * 0.05);
      colors.set([col.r, col.g, col.b], i * 3);
    }
    geo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
    geo.computeVertexNormals();
    const m = new THREE.Mesh(geo, new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.95 }));
    m.receiveShadow = true;
    return m;
  }

  private rocks(): THREE.InstancedMesh {
    const geo = new THREE.DodecahedronGeometry(1, 1);
    const p = geo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const v = new THREE.Vector3().fromBufferAttribute(p, i);
      v.multiplyScalar(0.75 + noise(v.x * 2 + 5, v.z * 2 + v.y) * 0.5);
      p.setXYZ(i, v.x, v.y * 0.7, v.z);
    }
    geo.computeVertexNormals();
    const n = 160;
    const mesh = new THREE.InstancedMesh(geo, new THREE.MeshStandardMaterial({ color: 0x8a8378, roughness: 0.9 }), n);
    const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3();
    for (let i = 0; i < n; i++) {
      const z = -i * 1.2 + 14;
      const x = centre(z) + (hash(i, 3) - 0.5) * (BED_HALF_WIDTH * 2.6);
      const r = 0.15 + hash(i, 7) ** 2 * 0.7;
      if (Math.abs(x - GAUGE_POS.x) < 1.2 && Math.abs(z - GAUGE_POS.z) < 1.2) continue;
      t.set(x, groundHeight(x, z) + r * 0.2, z);
      q.setFromEuler(new THREE.Euler(hash(i, 1) * 3, hash(i, 2) * 3, hash(i, 4) * 3));
      s.setScalar(r);
      mesh.setMatrixAt(i, m.compose(t, q, s));
      mesh.setColorAt(i, new THREE.Color().setHSL(0.08, 0.08, 0.35 + hash(i, 9) * 0.2));
    }
    mesh.castShadow = mesh.receiveShadow = true;
    return mesh;
  }

  private trees(): THREE.Group {
    const g = new THREE.Group();
    const trunkGeo = new THREE.CylinderGeometry(0.12, 0.2, 2.4, 6);
    const crownGeo = new THREE.IcosahedronGeometry(1.6, 1);
    const n = 260;
    const trunks = new THREE.InstancedMesh(trunkGeo, new THREE.MeshStandardMaterial({ color: 0x4a3626 }), n);
    const crowns = new THREE.InstancedMesh(crownGeo, new THREE.MeshStandardMaterial({ color: 0x3d6b2a, flatShading: true }), n);
    const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3();
    let k = 0;
    for (let i = 0; k < n && i < 4000; i++) {
      const x = (hash(i, 11) - 0.5) * 130, z = 20 - hash(i, 13) * 230;
      const d = Math.abs(x - centre(z));
      if (d < BED_HALF_WIDTH + 4.5 || (Math.abs(z - 12) < 10 && Math.abs(x) < 8)) continue; // clear bed and camera
      const y = groundHeight(x, z), sc = 0.7 + hash(i, 17) * 0.9;
      trunks.setMatrixAt(k, m.compose(t.set(x, y + 1.2 * sc, z), q.identity(), s.setScalar(sc)));
      crowns.setMatrixAt(k, m.compose(t.set(x, y + 2.9 * sc, z), q.setFromEuler(new THREE.Euler(0, i, 0)), s.set(sc, sc * (1.1 + hash(i, 19) * 0.6), sc)));
      crowns.setColorAt(k, new THREE.Color().setHSL(0.26 + hash(i, 23) * 0.06, 0.45, 0.22 + hash(i, 29) * 0.1));
      k++;
    }
    trunks.count = crowns.count = k;
    trunks.castShadow = crowns.castShadow = true;
    g.add(trunks, crowns);
    return g;
  }

  private makeGauge(): THREE.Group {
    const g = new THREE.Group();
    const tex = gaugeTexture(GAUGE_HEIGHT);
    const w = GAUGE_WIDTH;
    const board = new THREE.Mesh(new THREE.PlaneGeometry(w, GAUGE_HEIGHT), new THREE.MeshStandardMaterial({ map: tex, roughness: 0.6 }));
    board.position.y = GAUGE_HEIGHT / 2;
    board.position.z = 0.09;
    const post = new THREE.Mesh(new THREE.BoxGeometry(w + 0.08, GAUGE_HEIGHT + 0.6, 0.16), new THREE.MeshStandardMaterial({ color: 0x5b5f63, roughness: 0.8 }));
    post.position.y = (GAUGE_HEIGHT + 0.6) / 2 - 0.4;
    post.castShadow = true;
    g.add(post, board);
    g.position.copy(GAUGE_POS);
    g.lookAt(GAUGE_POS.x - 3, GAUGE_POS.y, GAUGE_POS.z + 14); // face the camera node, no tilt
    return g;
  }

  // What the water reflects: the valley (banks, trees, sky) seen from just
  // above the stream, prefiltered for one renderer. An environment map belongs
  // to one WebGL context, so the main view and the camera node each make their own.
  reflection(renderer: THREE.WebGLRenderer): THREE.Texture {
    this.water.visible = false;
    const at = new THREE.Vector3(GAUGE_POS.x - 2.5, 0.6, GAUGE_POS.z + 2);
    const env = new THREE.PMREMGenerator(renderer).fromScene(this.scene, 0.02, 0.1, 400, { position: at }).texture;
    this.water.visible = true;
    return env;
  }

  setWaterEnvironment(env: THREE.Texture) {
    this.water.material.envMap = env;
  }

  // Board corners in world space (for the twin's own two-point calibration).
  gaugePoint(m: number, side: -1 | 0 | 1 = 0): THREE.Vector3 {
    this.gauge.updateMatrixWorld();
    return new THREE.Vector3((side * GAUGE_WIDTH) / 2, m, 0.1).applyMatrix4(this.gauge.matrixWorld);
  }

  update(state: WorldState, dt: number) {
    this.water.position.y = state.levelM;
    // The stream flows toward the camera; higher water flows faster.
    this.waves.offset.y += dt * (0.05 + state.levelM * 0.08);
    this.waves.offset.x = Math.sin(performance.now() / 4000) * 0.02;
    const m = this.water.material;
    m.color.copy(this.clearColor).lerp(this.mudColor, state.mud);
    m.normalScale.setScalar(0.6 + state.levelM * 0.35);
    m.roughness = 0.12 + state.mud * 0.12;
    // Muddy water is opaque and dull: less of the valley shows in it.
    m.envMapIntensity = 0.85 - state.mud * 0.6;
  }
}
