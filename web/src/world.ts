import * as THREE from "three";
import { Sky } from "three/addons/objects/Sky.js";
import { mergeGeometries } from "three/addons/utils/BufferGeometryUtils.js";
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
  rain: number; // 0 dry .. 1 downpour
  night: number; // 0 noon .. 1 night
  people: number; // people standing on the banks near the gauge, 0-3
  logs: boolean; // logs and branches carried by the stream
  debrisFlow: boolean; // "avenida torrencial": thick mud carrying rocks, trees and wreckage
  dam: boolean; // logs and rocks piled across the channel upstream, holding the water back
}

// Where the people stand: on the banks the camera node sees, close to the water.
const PEOPLE_SPOTS: [number, number][] = [
  [-BED_HALF_WIDTH - 0.6, -3], // left bank, opposite the gauge
  [-BED_HALF_WIDTH - 1.4, 1.5],
  [BED_HALF_WIDTH + 0.9, -9.5], // right bank, upstream of the gauge
];
const CLOTHES = [0xd8452f, 0xf2c230, 0x2f7fd8];
const LOGS = 9;
// What a debris flow carries, besides logs: boulders, planks, roof sheets,
// water tanks and tyres.
const WRECK = { rocks: 18, planks: 10, sheets: 6, tanks: 3, tyres: 3 };
const RAIN_DROPS = 2400;

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
  private hemi = new THREE.HemisphereLight(0xcfe3ff, 0x4a5a2a, 1.1);
  private fog = new THREE.Fog(0xb9cdd6, 60, 180);
  private dayFog = new THREE.Color(0xb9cdd6);
  private rainFog = new THREE.Color(0x8d989e);
  private nightFog = new THREE.Color(0x0b1220);
  private people: THREE.Group[] = [];
  private peopleBase: { x: number; z: number; out: number }[] = []; // spot and direction away from the water
  private logs: THREE.InstancedMesh;
  private roots: THREE.InstancedMesh; // root wads on some logs: whole trees torn from the banks
  private crowns: THREE.InstancedMesh; // and their leafy tops at the other end
  private logZ: number[] = [];
  private wreck: { mesh: THREE.InstancedMesh; z: number[]; size: THREE.Vector3[]; lift: number }[] = [];
  private debrisColor = new THREE.Color(0x5e4a38);
  private dam = new THREE.Group();
  private rain: THREE.LineSegments<THREE.BufferGeometry, THREE.LineBasicMaterial>;
  /** Tone-mapping exposure for the renderers: night makes everything darker. */
  exposure = 1;
  /** 0 noon .. 1 night, for the camera node's infrared. */
  night = 0;

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
    this.scene.add(this.sun, this.hemi);
    this.scene.fog = this.fog;
    this.scene.background = new THREE.Color(0x0b1220); // seen only when the sky is off at night

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

    for (let i = 0; i < PEOPLE_SPOTS.length; i++) {
      const [dx, dz] = PEOPLE_SPOTS[i];
      const z = GAUGE_POS.z + dz, x = centre(z) + dx;
      const p = this.person(CLOTHES[i]);
      p.position.set(x, groundHeight(x, z), z);
      p.lookAt(centre(z), p.position.y, z); // facing the water
      p.visible = false;
      this.peopleBase.push({ x, z, out: Math.sign(dx) });
      this.people.push(p);
      this.scene.add(p);
    }
    this.logs = this.makeLogs();
    this.roots = this.makeRoots();
    this.crowns = this.makeCrowns();
    this.scene.add(this.logs, this.roots, this.crowns);
    this.makeDam();
    this.scene.add(this.dam);
    for (const w of this.makeWreck()) {
      this.wreck.push(w);
      this.scene.add(w.mesh);
    }
    this.rain = this.makeRain();
    this.scene.add(this.rain);
  }

  // A low-poly person, about 1.7 m: legs, a coloured jacket, head. Bright
  // clothes, as people on a riverbank usually look from a camera on a pole.
  private person(color: number): THREE.Group {
    const g = new THREE.Group();
    const skin = new THREE.MeshStandardMaterial({ color: 0x8d5a3b, roughness: 0.8 });
    const jacket = new THREE.MeshStandardMaterial({ color, roughness: 0.7 });
    const trousers = new THREE.MeshStandardMaterial({ color: 0x2b3240, roughness: 0.9 });
    for (const side of [-0.1, 0.1]) {
      const leg = new THREE.Mesh(new THREE.CylinderGeometry(0.08, 0.07, 0.85, 6), trousers);
      leg.position.set(side, 0.43, 0);
      g.add(leg);
    }
    const body = new THREE.Mesh(new THREE.CylinderGeometry(0.2, 0.24, 0.62, 8), jacket);
    body.position.y = 1.16;
    const head = new THREE.Mesh(new THREE.SphereGeometry(0.13, 10, 8), skin);
    head.position.y = 1.6;
    for (const side of [-0.27, 0.27]) {
      const arm = new THREE.Mesh(new THREE.CylinderGeometry(0.06, 0.05, 0.6, 6), jacket);
      arm.position.set(side, 1.12, 0);
      arm.rotation.z = side > 0 ? 0.12 : -0.12;
      g.add(arm);
    }
    g.add(body, head);
    g.traverse((o) => (o.castShadow = true));
    return g;
  }

  // Logs and branches carried by the flood, drifting toward the camera: from
  // branches to whole trunks, each with its own length, girth and spin. The
  // trunk is knobbly bark with pale cut ends, in one draw call.
  private makeLogs(): THREE.InstancedMesh {
    const geo = new THREE.CylinderGeometry(0.85, 1, 1, 7, 4);
    const p = geo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const x = p.getX(i), y = p.getY(i), z = p.getZ(i);
      const k = 0.88 + noise(Math.atan2(z, x) * 2 + 7, y * 5 + 3) * 0.24; // knots and bends in the bark
      p.setXYZ(i, x * k, y, z * k);
    }
    geo.computeVertexNormals();
    geo.rotateZ(Math.PI / 2);
    const bark = new THREE.MeshStandardMaterial({ color: 0x9c8670, roughness: 1, flatShading: true });
    const cut = new THREE.MeshStandardMaterial({ color: 0xe8cfa6, roughness: 0.9 });
    const mesh = new THREE.InstancedMesh(geo, [bark, cut, cut], LOGS);
    for (let i = 0; i < LOGS; i++) {
      this.logZ.push(GAUGE_POS.z - 8 - i * 8 - hash(i, 61) * 6);
      mesh.setColorAt(i, new THREE.Color().setHSL(0.07 + hash(i, 69) * 0.03, 0.38, 0.13 + hash(i, 67) * 0.12));
    }
    mesh.visible = false;
    return mesh;
  }

  // A root wad: a clod of earth with roots splayed around it, facing along +x
  // so it sits square on the end of a trunk.
  // A natural dam: trunks jammed across the channel upstream of the gauge, with
  // rocks and mud between them. The water behind it rises out of sight.
  private makeDam() {
    const z0 = GAUGE_POS.z - 22, x0 = centre(z0);
    const wood = new THREE.MeshStandardMaterial({ color: 0x4e3b2a, roughness: 0.95 });
    const rock = new THREE.MeshStandardMaterial({ color: 0x6f6a62, roughness: 0.9, flatShading: true });
    const mud = new THREE.MeshStandardMaterial({ color: 0x5e4a38, roughness: 1 });
    for (let i = 0; i < 9; i++) {
      const len = 6 + hash(i, 131) * 3, r = 0.15 + hash(i, 137) * 0.18;
      const log = new THREE.Mesh(new THREE.CylinderGeometry(r * 0.85, r, len, 7), wood);
      log.rotation.set(Math.PI / 2 + (hash(i, 139) - 0.5) * 0.5, 0, Math.PI / 2 + (hash(i, 149) - 0.5) * 0.6);
      log.position.set(x0 + (hash(i, 151) - 0.5) * 1.5, 0.2 + (i % 4) * 0.32, z0 + (hash(i, 157) - 0.5) * 1.6);
      this.dam.add(log);
    }
    for (let i = 0; i < 14; i++) {
      const s = 0.3 + hash(i, 163) * 0.6;
      const r = new THREE.Mesh(new THREE.DodecahedronGeometry(s, 0), rock);
      r.position.set(x0 + (hash(i, 167) - 0.5) * 6, s * 0.5 + hash(i, 173) * 0.6, z0 + (hash(i, 179) - 0.5) * 2.2);
      this.dam.add(r);
    }
    const bank = new THREE.Mesh(new THREE.BoxGeometry(BED_HALF_WIDTH * 2.4, 1.1, 2.2), mud);
    bank.position.set(x0, 0.4, z0);
    this.dam.add(bank);
    this.dam.traverse((o) => (o.castShadow = o.receiveShadow = true));
    this.dam.visible = false;
  }

  private makeRoots(): THREE.InstancedMesh {
    const parts: THREE.BufferGeometry[] = [];
    const clod = new THREE.IcosahedronGeometry(0.55, 0);
    clod.scale(0.45, 1, 1);
    parts.push(clod);
    for (let k = 0; k < 7; k++) {
      const root = new THREE.ConeGeometry(0.1, 0.8 + hash(k, 5) * 0.5, 4).toNonIndexed();
      root.translate(0, 0.45, 0);
      root.rotateZ(-0.5 - hash(k, 9) * 0.5); // swept back, away from the trunk
      root.rotateX((k / 7) * Math.PI * 2 + hash(k, 3) * 0.6);
      parts.push(root);
    }
    const geo = mergeGeometries(parts.map((g) => { g.deleteAttribute("uv"); return g; }))!;
    geo.computeVertexNormals();
    const mesh = new THREE.InstancedMesh(geo, new THREE.MeshStandardMaterial({ color: 0x6b5038, roughness: 1, flatShading: true }), LOGS);
    mesh.visible = false;
    return mesh;
  }

  // The leafy top of a whole tree, battered and muddy.
  private makeCrowns(): THREE.InstancedMesh {
    const geo = new THREE.IcosahedronGeometry(1, 1);
    const p = geo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const v = new THREE.Vector3().fromBufferAttribute(p, i);
      v.multiplyScalar(0.7 + noise(v.x * 2 + 11, v.y * 2 + v.z * 2) * 0.6);
      p.setXYZ(i, v.x * 1.4, v.y * 0.6, v.z);
    }
    geo.computeVertexNormals();
    const mesh = new THREE.InstancedMesh(geo, new THREE.MeshStandardMaterial({ color: 0x4d5f2a, roughness: 1, flatShading: true }), LOGS);
    mesh.visible = false;
    return mesh;
  }

  private makeWreck() {
    const rock = (seed: number) => rockGeometry(seed);
    const tint = (list: number[]) => (i: number) => new THREE.Color(list[Math.floor(hash(i, 113) * list.length)]).offsetHSL(0, 0, (hash(i, 127) - 0.5) * 0.08);
    const n = Math.ceil(WRECK.rocks / 3);
    const stone = new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 0.95, flatShading: true, vertexColors: true });
    const rockSize = (i: number) => {
      const r = 0.35 + hash(i, 71) ** 2 * 0.9;
      return new THREE.Vector3(r * (0.9 + hash(i, 72) * 0.4), r * (0.6 + hash(i, 74) * 0.3), r * (0.8 + hash(i, 76) * 0.4));
    };
    const rockColor = tint([0x8a8478, 0x7a6f62, 0x6b675f, 0x93826b, 0x5f5a52]);
    const kinds = [
      // Boulders ride mostly under the slurry: only their tops show.
      ...[0, 1, 2].map((v) => ({ n, geo: rock(v), mat: stone, size: rockSize, color: rockColor, lift: -0.15 })),
      { n: WRECK.planks, geo: new THREE.BoxGeometry(1, 1, 1), mat: new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 0.85 }),
        size: (i: number) => new THREE.Vector3(0.9 + hash(i, 73) * 2.1, 0.04 + hash(i, 75) * 0.03, 0.12 + hash(i, 79) * 0.14),
        color: tint([0xa07a52, 0x8c6a48, 0x9e9a90, 0x5f7d8c, 0x6f8a5a, 0xd8d2c4]), lift: 0 },
      { n: WRECK.sheets, geo: corrugatedSheet(), mat: new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 0.5, metalness: 0.55, side: THREE.DoubleSide }),
        size: (i: number) => new THREE.Vector3(0.8 + hash(i, 83) * 0.3, 1, 1.6 + hash(i, 89) * 1.1),
        color: tint([0xb8c0c4, 0xa3abb0, 0x8f5a3c, 0x9a6a48, 0x7d8f9c]), lift: 0.05 },
      { n: WRECK.tanks, geo: new THREE.CylinderGeometry(1, 1, 1, 12).rotateZ(Math.PI / 2), mat: new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 0.45 }),
        size: (i: number) => { const r = 0.28 + hash(i, 131) * 0.18; return new THREE.Vector3(r * 3, r, r); },
        color: tint([0x2a5d9e, 0x1f4f8a, 0x2b2b2b, 0xd9d4c8]), lift: 0.15 },
      { n: WRECK.tyres, geo: new THREE.TorusGeometry(1, 0.38, 6, 12).rotateX(Math.PI / 2), mat: new THREE.MeshStandardMaterial({ color: 0x1f1f1f, roughness: 0.8 }),
        size: (i: number) => new THREE.Vector3().setScalar(0.3 + hash(i, 137) * 0.08), color: () => new THREE.Color(0xffffff), lift: 0.3 },
    ];
    return kinds.map((k, j) => {
      const mesh = new THREE.InstancedMesh(k.geo, k.mat, k.n);
      mesh.visible = false;
      const z: number[] = [], size: THREE.Vector3[] = [];
      for (let i = 0; i < k.n; i++) {
        z.push(GAUGE_POS.z - 4 - hash(i, 97 + j) * 60);
        size.push(k.size(i + j * 31));
        mesh.setColorAt(i, k.color(i + j * 31));
      }
      return { mesh, z, size, lift: k.lift };
    });
  }

  private makeRain(): THREE.LineSegments<THREE.BufferGeometry, THREE.LineBasicMaterial> {
    const pos = new Float32Array(RAIN_DROPS * 6);
    for (let i = 0; i < RAIN_DROPS; i++) {
      const x = GAUGE_POS.x - 18 + hash(i, 31) * 30, z = GAUGE_POS.z - 26 + hash(i, 37) * 44, y = hash(i, 41) * 16;
      pos.set([x, y, z, x - 0.04, y - 0.45, z + 0.02], i * 6);
    }
    const geo = new THREE.BufferGeometry();
    geo.setAttribute("position", new THREE.BufferAttribute(pos, 3));
    const lines = new THREE.LineSegments(geo, new THREE.LineBasicMaterial({ color: 0xc8d4dc, transparent: true, opacity: 0.55 }));
    lines.frustumCulled = false;
    lines.visible = false;
    return lines;
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
    this.updateWeather(state, dt);
    this.updateProps(state, dt);
    this.water.position.y = state.levelM;
    // The stream flows toward the camera; higher water flows faster.
    this.waves.offset.y += dt * (0.05 + state.levelM * 0.08);
    this.waves.offset.x = Math.sin(performance.now() / 4000) * 0.02;
    const m = this.water.material;
    m.color.copy(this.clearColor).lerp(this.mudColor, state.mud);
    m.normalScale.setScalar(0.6 + state.levelM * 0.35);
    m.roughness = 0.12 + state.mud * 0.12;
    // Muddy water is opaque and dull: less of the valley shows in it.
    m.envMapIntensity = (0.85 - state.mud * 0.6) * (1 - state.night * 0.85);
    if (state.debrisFlow) {
      // A debris flow is a slurry, not water: thick, lumpy, the colour of the
      // slope it tore away, with the wet sheen of mud in motion.
      m.color.copy(this.debrisColor);
      m.roughness = 0.85;
      m.envMapIntensity = 0.05;
      m.normalScale.setScalar(2.2);
    }
  }

  // Rain greys the sky and closes the fog; night takes the light away, and
  // with it the camera's view of the water.
  private updateWeather(state: WorldState, dt: number) {
    const n = state.night, r = state.rain;
    const light = 1 - n * 0.97;
    this.sun.intensity = 2.6 * light * (1 - r * 0.7);
    this.hemi.intensity = 1.1 * (1 - n * 0.9) * (1 - r * 0.25);
    this.sky.visible = n < 0.55;
    this.fog.color.copy(this.dayFog).lerp(this.rainFog, r).lerp(this.nightFog, n);
    (this.scene.background as THREE.Color).copy(this.nightFog);
    this.fog.near = 60 - r * 45;
    this.fog.far = 180 - r * 110;
    this.exposure = 1 - n * 0.75;
    this.night = n;

    this.rain.visible = r > 0.02;
    if (!this.rain.visible) return;
    this.rain.material.opacity = 0.25 + r * 0.45;
    const p = this.rain.geometry.attributes.position as THREE.BufferAttribute;
    const fall = dt * (14 + r * 6);
    const shown = Math.floor(RAIN_DROPS * r);
    for (let i = 0; i < RAIN_DROPS; i++) {
      let y = p.getY(i * 2) - fall;
      if (y < state.levelM - 0.2 || i >= shown) y = i >= shown ? -50 : 16;
      p.setY(i * 2, y);
      p.setY(i * 2 + 1, y - 0.45);
    }
    p.needsUpdate = true;
  }

  private updateProps(state: WorldState, dt: number) {
    this.dam.visible = state.dam;
    this.people.forEach((p, i) => {
      p.visible = i < state.people;
      // People keep their feet dry: they step back up the bank as the water
      // rises, and stay close to the edge, which is what puts them at risk.
      const b = this.peopleBase[i];
      let x = b.x;
      for (let k = 0; k < 40 && groundHeight(x, b.z) < state.levelM + 0.25; k++) x += b.out * 0.25;
      p.position.set(x, groundHeight(x, b.z), b.z);
    });
    const carried = state.levelM > 0.35;
    this.logs.visible = this.roots.visible = this.crowns.visible = (state.logs || state.debrisFlow) && carried;
    for (const w of this.wreck) w.mesh.visible = state.debrisFlow && carried;
    if (!this.logs.visible) return;
    const speed = 1.2 + state.levelM * 1.1; // m/s: higher water is faster
    const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3();
    const now = performance.now();
    // A plain flood carries a few logs; a debris flow carries all of them, and whole trees.
    const shown = state.debrisFlow ? LOGS : 5;
    for (let i = 0; i < LOGS; i++) {
      this.logZ[i] += speed * dt * (0.8 + hash(i, 53) * 0.4);
      if (this.logZ[i] > GAUGE_POS.z + 16) this.logZ[i] -= 75;
      const z = this.logZ[i], wobble = Math.sin(now / 700 + i * 2);
      const x = centre(z) + (hash(i, 51) - 0.5) * 3.4;
      // Every third one is a whole tree: long, with its root wad at one end and
      // its battered crown at the other.
      const tree = state.debrisFlow && i % 3 === 0 && i < shown;
      const len = tree ? 4 + hash(i, 57) * 2.5 : 1.2 + hash(i, 57) * 4.3;
      const girth = tree ? 0.22 + hash(i, 59) * 0.12 : 0.08 + hash(i, 59) ** 1.5 * 0.32;
      t.set(x, state.levelM + girth * 0.4, z);
      q.setFromEuler(new THREE.Euler(wobble * 0.12, hash(i, 63) * Math.PI + now / (4000 + i * 900), wobble * 0.08));
      s.set(i < shown ? len : 0.0001, girth, girth);
      this.logs.setMatrixAt(i, m.compose(t, q, s));
      const end = new THREE.Vector3(len / 2, 0, 0).applyQuaternion(q).add(t);
      s.setScalar(tree ? girth * 4.5 : 0.0001);
      this.roots.setMatrixAt(i, m.compose(end, q, s));
      const top = new THREE.Vector3(-len / 2 - 0.6, 0.25, 0).applyQuaternion(q).add(t);
      s.set(1.1, 1, 1.1).multiplyScalar(tree ? 1 : 0.0001);
      this.crowns.setMatrixAt(i, m.compose(top, q, s));
    }
    this.logs.instanceMatrix.needsUpdate = this.roots.instanceMatrix.needsUpdate = this.crowns.instanceMatrix.needsUpdate = true;
    if (!state.debrisFlow) return;
    this.wreck.forEach((w, j) => {
      for (let i = 0; i < w.z.length; i++) {
        w.z[i] += speed * dt * (0.7 + hash(i, 101 + j) * 0.5);
        if (w.z[i] > GAUGE_POS.z + 16) w.z[i] -= 70;
        const z = w.z[i], bob = Math.sin(now / 500 + i * 1.7 + j);
        const x = centre(z) + (hash(i, 103 + j) - 0.5) * 4.2;
        t.set(x, state.levelM + w.size[i].y * w.lift + bob * 0.05, z);
        q.setFromEuler(new THREE.Euler(bob * 0.25 + hash(i, 107) * 0.4, hash(i, 109 + j) * 6 + now / 3000, bob * 0.2));
        w.mesh.setMatrixAt(i, m.compose(t, q, w.size[i]));
      }
      w.mesh.instanceMatrix.needsUpdate = true;
    });
  }
}

// A boulder: a lumpy, flat-faced stone, wet and muddy on its lower half, each
// face a slightly different shade. Three seeds give three shapes.
function rockGeometry(seed: number): THREE.BufferGeometry {
  const geo = new THREE.IcosahedronGeometry(1, 1);
  const p = geo.attributes.position as THREE.BufferAttribute;
  const v = new THREE.Vector3();
  for (let i = 0; i < p.count; i++) {
    v.fromBufferAttribute(p, i);
    v.multiplyScalar(0.7 + noise(v.x * 1.7 + seed * 13, v.y * 1.7 + v.z * 1.3 + seed * 7) * 0.6);
    p.setXYZ(i, v.x, v.y, v.z);
  }
  const colors = new Float32Array(p.count * 3);
  const mud = new THREE.Color(0x6a5238), c = new THREE.Color();
  for (let f = 0; f < p.count; f += 3) {
    const y = (p.getY(f) + p.getY(f + 1) + p.getY(f + 2)) / 3;
    c.setScalar(0.85 + hash(f, seed + 1) * 0.3).lerp(mud, THREE.MathUtils.smoothstep(-y, -0.1, 0.5) * 0.8);
    for (let k = 0; k < 3; k++) colors.set([c.r, c.g, c.b], (f + k) * 3);
  }
  geo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
  geo.computeVertexNormals();
  return geo;
}

// A corrugated roof sheet ("zinc"), slightly bent by the flood: 1 m wide in
// x, 1 m long in z, ridges along its length.
function corrugatedSheet(): THREE.BufferGeometry {
  const geo = new THREE.PlaneGeometry(1, 1, 24, 3);
  geo.rotateX(-Math.PI / 2);
  const p = geo.attributes.position as THREE.BufferAttribute;
  for (let i = 0; i < p.count; i++) {
    const x = p.getX(i), z = p.getZ(i);
    p.setY(i, Math.sin(x * Math.PI * 16) * 0.018 + z * z * 0.25 + x * z * 0.12);
  }
  geo.computeVertexNormals();
  return geo;
}
