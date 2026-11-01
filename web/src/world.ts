import * as THREE from "three";
import { Sky } from "three/addons/objects/Sky.js";
import { mergeGeometries } from "three/addons/utils/BufferGeometryUtils.js";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
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

// A footpath runs along each bank, a little above the stream: where people
// walk, and where a flash flood finds them.
const PATH_IN = BED_HALF_WIDTH + 0.5, PATH_OUT = BED_HALF_WIDTH + 2.1, PATH_H = 0.8;

// How much of the ground at distance d from the channel centre is footpath (0..1).
export function pathWeight(d: number): number {
  return THREE.MathUtils.smoothstep(d, PATH_IN - 0.5, PATH_IN) * (1 - THREE.MathUtils.smoothstep(d, PATH_OUT, PATH_OUT + 1.1));
}

// The valley without its paths.
function natural(x: number, z: number): number {
  const d = Math.abs(x - centre(z));
  const bed = -0.15 + fbm(x * 0.8, z * 0.8) * 0.3;
  if (d < BED_HALF_WIDTH) return bed;
  const bank = THREE.MathUtils.smoothstep(d, BED_HALF_WIDTH, BED_HALF_WIDTH + 5) * 4.2;
  const hills = Math.max(0, d - BED_HALF_WIDTH - 5) * 0.45 + fbm(x * 0.12, z * 0.12) * 6 * THREE.MathUtils.smoothstep(d, 8, 20);
  return bed + bank + hills + fbm(x * 0.5, z * 0.5) * 0.6 * THREE.MathUtils.smoothstep(d, BED_HALF_WIDTH, 6);
}

// A footbridge crosses upstream of the gauge; a big flood reaches it. Next to
// it, on each bank, a trail climbs from the footpath to the top of the bank:
// the way out when the water comes. In (distance from the channel centre, z):
// its foot on the path, its top, and a flat landing where people wait.
export const BRIDGE_Z = GAUGE_POS.z - 15, BRIDGE_DECK = 2.6;
export const TRAIL = {
  foot: { d: PATH_IN + 0.8, z: BRIDGE_Z + 8 },
  top: { d: BED_HALF_WIDTH + 7.5, z: BRIDGE_Z + 2 },
  landing: { d: BED_HALF_WIDTH + 8.4, z: BRIDGE_Z + 1.6 },
};
const trailTop = (side: number) => natural(centre(TRAIL.top.z) + side * TRAIL.top.d, TRAIL.top.z);
const TRAIL_TOP = [trailTop(-1), trailTop(1)];

// How much the ground at (d, z) is trail (0..1), and how far up it (0..1).
export function trailAt(d: number, z: number): { w: number; t: number } {
  const { foot: a, top: b, landing: l } = TRAIL;
  const ex = b.d - a.d, ez = b.z - a.z;
  const t = THREE.MathUtils.clamp(((d - a.d) * ex + (z - a.z) * ez) / (ex * ex + ez * ez), 0, 1);
  const off = Math.hypot(d - (a.d + ex * t), z - (a.z + ez * t));
  const ramp = (1 - THREE.MathUtils.smoothstep(off, 0.6, 1.3)) * THREE.MathUtils.smoothstep(d, PATH_IN - 0.2, PATH_IN + 0.3);
  const flat = 1 - THREE.MathUtils.smoothstep(Math.hypot(d - l.d, z - l.z), 1.4, 2.2);
  return flat > ramp ? { w: flat, t: 1 } : { w: ramp, t };
}

export function groundHeight(x: number, z: number): number {
  const d = Math.abs(x - centre(z));
  const h = natural(x, z);
  if (d < BED_HALF_WIDTH) return h;
  const onPath = THREE.MathUtils.lerp(h, PATH_H + fbm(x * 0.6, z * 0.6) * 0.06, pathWeight(d));
  const tr = trailAt(d, z);
  if (tr.w <= 0) return onPath;
  const top = TRAIL_TOP[x < centre(z) ? 0 : 1];
  return THREE.MathUtils.lerp(onPath, THREE.MathUtils.lerp(PATH_H, top, tr.t), tr.w);
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
  evacuate: boolean; // the station's alert is at evacuate: people on the banks leave by the trails
}

// Where the people stand: on the banks the camera node sees, close to the water.
const PEOPLE_SPOTS: [number, number][] = [
  [-BED_HALF_WIDTH - 0.6, -3], // left bank, opposite the gauge
  [-BED_HALF_WIDTH - 1.3, -7], // further up the left bank, clear of the camera's foreground
  [BED_HALF_WIDTH + 0.9, -9.5], // right bank, upstream of the gauge
];
const CLOTHES = [0xd8452f, 0xf2c230, 0x2f7fd8];
// The walkers that replace the simple figures once loaded (made with Meshy for
// Vigiax, see ASSETS.md): their walks are in place, the twin moves them.
const WALKERS = [
  { url: "/models/woman.glb", height: 1.62, speed: 0.55 }, // filming the stream with her phone
  { url: "/models/farmer.glb", height: 1.7, speed: 0.35 },
  { url: "/models/boy.glb", height: 1.52, speed: 0.6 },
];
const PACE_M = 3; // each walks this stretch of bank, up and back
const HURRY = 1.5; // m/s up the trail
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
  private fog = new THREE.Fog(0xc2d2d8, 30, 160);
  private dayFog = new THREE.Color(0xc2d2d8);
  private rainFog = new THREE.Color(0x8d989e);
  private nightFog = new THREE.Color(0x0b1220);
  private people: THREE.Group[] = [];
  private peopleBase: { dx: number; z: number; out: number }[] = []; // spot (from the channel centre) and direction away from the water
  private mixers: (THREE.AnimationMixer | undefined)[] = [];
  // Where each walker is: pacing (u, 0..1 of the round trip) or on the way out
  // (e, metres along the route from where they left the path).
  private walkers = WALKERS.map((_, i) => ({ u: i * 0.3, e: 0, from: { d: 0, z: 0 } }));
  private logs: THREE.InstancedMesh;
  private roots: THREE.InstancedMesh; // root wads on some logs: whole trees torn from the banks
  private crowns: THREE.InstancedMesh; // and their leafy tops at the other end
  private logZ: number[] = [];
  private wreck: { mesh: THREE.InstancedMesh; z: number[]; size: THREE.Vector3[]; lift: number }[] = [];
  private debrisColor = new THREE.Color(0x5e4a38);
  private dam = new THREE.Group();
  private rain: THREE.LineSegments<THREE.BufferGeometry, THREE.LineBasicMaterial>;
  private mist: THREE.Sprite[] = [];
  private waterTime = { value: 0 };
  private foam = { value: 0.7 };
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
    // A morning sun, low enough that trees and banks throw long shadows.
    const sunDir = this.sunDir.setFromSphericalCoords(1, THREE.MathUtils.degToRad(64), THREE.MathUtils.degToRad(140));
    u.sunPosition.value.copy(sunDir);
    this.scene.add(this.sky);

    // Shadows cover the stretch of valley both cameras see; computed once.
    this.sun.target.position.set(GAUGE_POS.x - 4, 0, GAUGE_POS.z - 14);
    this.sun.position.copy(sunDir).multiplyScalar(90).add(this.sun.target.position);
    this.sun.castShadow = true;
    this.sun.shadow.mapSize.set(4096, 4096);
    this.sun.shadow.radius = 3;
    this.sun.shadow.normalBias = 0.03;
    Object.assign(this.sun.shadow.camera, { left: -48, right: 48, top: 48, bottom: -48, far: 220 });
    this.scene.add(this.sun, this.sun.target, this.hemi);
    this.scene.fog = this.fog;
    this.scene.background = new THREE.Color(0x0b1220); // seen only when the sky is off at night

    this.scene.add(this.terrain());
    this.scene.add(this.rocks());
    this.scene.add(this.trees());
    this.scene.add(this.bushes());
    for (const m of this.makeMist()) {
      this.mist.push(m);
      this.scene.add(m);
    }

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
    this.water.material.onBeforeCompile = (sh) => foamShader(sh, this.waterTime, this.foam);
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
      this.peopleBase.push({ dx, z, out: Math.sign(dx) });
      this.people.push(p);
      this.scene.add(p);
      this.loadWalker(i);
    }
    this.loadBridge();
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

  private loadWalker(i: number) {
    const w = WALKERS[i];
    new GLTFLoader().load(w.url, (gltf) => {
      const model = gltf.scene;
      model.scale.setScalar(w.height / 1.7); // the models are 1.7 m tall
      model.traverse((o) => {
        o.castShadow = true;
        o.frustumCulled = false; // a skinned mesh's bounds do not follow its pose
      });
      this.people[i].clear();
      this.people[i].add(model);
      const mixer = new THREE.AnimationMixer(model);
      mixer.clipAction(gltf.animations[0]).play();
      mixer.update(i * 0.7); // out of step with each other
      this.mixers[i] = mixer;
    });
  }

  // The footbridge spans the channel bank to bank at deck height.
  private loadBridge() {
    new GLTFLoader().load("/models/footbridge.glb", (gltf) => {
      const b = gltf.scene;
      const size = new THREE.Box3().setFromObject(b).getSize(new THREE.Vector3());
      const c = centre(BRIDGE_Z);
      const reach = (side: number) => {
        let d = BED_HALF_WIDTH;
        while (d < 20 && groundHeight(c + side * d, BRIDGE_Z) < BRIDGE_DECK) d += 0.1;
        return d;
      };
      const l = reach(-1), r = reach(1);
      b.scale.setScalar((l + r + 1.2) / size.x);
      const box = new THREE.Box3().setFromObject(b);
      b.position.set(c + (r - l) / 2, BRIDGE_DECK - (box.min.y + box.max.y) / 2, BRIDGE_Z);
      b.traverse((o) => (o.castShadow = o.receiveShadow = true));
      this.scene.add(b);
    });
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
    const gravel = new THREE.Color(0x6b6156), mud = new THREE.Color(0x6e5338), path = new THREE.Color(0x8a7258), grass = new THREE.Color(0x4f7a2f), dark = new THREE.Color(0x2f4f22);
    const col = new THREE.Color();
    for (let i = 0; i < pos.count; i++) {
      const x = pos.getX(i), z = pos.getZ(i);
      const h = groundHeight(x, z);
      pos.setY(i, h);
      const d = Math.abs(x - centre(z));
      if (d < BED_HALF_WIDTH) col.copy(gravel).lerp(mud, fbm(x, z));
      else if (pathWeight(d) > 0.55 || trailAt(d, z).w > 0.55) col.copy(path).lerp(mud, fbm(x * 2, z * 2) * 0.5); // trodden earth
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

  // The valley's trees: pine stands on the slopes, as on Antioquia's hills,
  // with pasture between them, and broadleaf trees near the stream. Each kind
  // is one instanced mesh (trunk and foliage merged, coloured per vertex).
  private trees(): THREE.Group {
    const g = new THREE.Group();
    const mat = new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.92, flatShading: true });
    const n = 360;
    const kinds = [pineGeometry(0), pineGeometry(1), broadleafGeometry(2)].map((geo) => new THREE.InstancedMesh(geo, mat, n));
    const count = [0, 0, 0];
    const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3();
    for (let i = 0; i < 12000; i++) {
      const x = (hash(i, 11) - 0.5) * 130, z = 20 - hash(i, 13) * 230;
      const d = Math.abs(x - centre(z));
      if (d < BED_HALF_WIDTH + 4.5 || (Math.abs(z - 12) < 10 && Math.abs(x) < 8)) continue; // clear bed and camera
      if (trailAt(d, z).w > 0 || Math.hypot(d - TRAIL.landing.d, z - TRAIL.landing.z) < 3.5) continue; // and the way out
      const near = d < 16;
      if (!near && fbm(x * 0.06 + 3, z * 0.06) < 0.36) continue; // pasture between the stands
      const k = near && hash(i, 31) < 0.6 ? 2 : hash(i, 37) < 0.55 ? 0 : 1;
      if (count[k] >= n) continue;
      const sc = k === 2 ? 0.75 + hash(i, 17) * 0.6 : 0.65 + hash(i, 17) * 0.75;
      t.set(x, groundHeight(x, z) - 0.15, z);
      q.setFromEuler(new THREE.Euler((hash(i, 41) - 0.5) * 0.06, hash(i, 43) * 6.28, (hash(i, 47) - 0.5) * 0.06));
      kinds[k].setMatrixAt(count[k], m.compose(t, q, s.set(sc, sc * (0.9 + hash(i, 19) * 0.3), sc)));
      kinds[k].setColorAt(count[k], new THREE.Color().setScalar(0.8 + hash(i, 23) * 0.35));
      count[k]++;
    }
    kinds.forEach((mesh, k) => {
      mesh.count = count[k];
      mesh.castShadow = mesh.receiveShadow = true;
      g.add(mesh);
    });
    return g;
  }

  // Bushes and grass tufts on the banks, between the water and the trees. They
  // keep clear of the gauge and of the camera's line to it.
  private bushes(): THREE.Group {
    const g = new THREE.Group();
    const bushGeo = new THREE.IcosahedronGeometry(1, 1);
    const p = bushGeo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const v = new THREE.Vector3().fromBufferAttribute(p, i);
      v.multiplyScalar(0.75 + noise(v.x * 2.3 + 1, v.y * 2.3 + v.z * 1.9) * 0.5);
      p.setXYZ(i, v.x, v.y * 0.7, v.z);
    }
    bushGeo.computeVertexNormals();
    const tuftGeo = mergeGeometries(Array.from({ length: 7 }, (_, k) => {
      const blade = new THREE.ConeGeometry(0.035, 0.45 + hash(k, 3) * 0.3, 3);
      blade.translate(0, 0.25, 0);
      blade.rotateZ((hash(k, 5) - 0.5) * 0.7);
      blade.rotateY((k / 7) * Math.PI * 2);
      blade.translate(Math.cos(k * 2.4) * 0.08, 0, Math.sin(k * 2.4) * 0.08);
      blade.deleteAttribute("uv");
      return blade;
    }))!;
    const bushes = new THREE.InstancedMesh(bushGeo, new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 1, flatShading: true }), 220);
    const tufts = new THREE.InstancedMesh(tuftGeo, new THREE.MeshStandardMaterial({ color: 0xffffff, roughness: 1 }), 1600);
    const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3();
    const clear = (x: number, z: number) =>
      Math.hypot(x - GAUGE_POS.x, z - GAUGE_POS.z) < 4 || (z > GAUGE_POS.z - 2 && z < GAUGE_POS.z + 14 && x > GAUGE_POS.x - 9 && x < GAUGE_POS.x - 2) ||
      z > GAUGE_POS.z + 9; // the 3D view's own foreground
    let nb = 0, nt = 0;
    for (let i = 0; i < 20000 && (nb < 220 || nt < 1600); i++) {
      const z = 16 - hash(i, 211) * 130, side = hash(i, 213) < 0.5 ? -1 : 1;
      const d = BED_HALF_WIDTH + 0.4 + hash(i, 217) ** 1.5 * 7;
      const x = centre(z) + side * d;
      if (clear(x, z) || pathWeight(d) > 0.3 || trailAt(d, z).w > 0.1) continue;
      const y = groundHeight(x, z);
      if (nb < 220 && d > BED_HALF_WIDTH + 1.6 && hash(i, 219) < 0.14) {
        const r = 0.28 + hash(i, 221) * 0.42;
        bushes.setMatrixAt(nb, m.compose(t.set(x, y + r * 0.35, z), q.setFromEuler(new THREE.Euler(0, hash(i, 223) * 6, 0)), s.set(r * 1.3, r, r * 1.2)));
        bushes.setColorAt(nb++, new THREE.Color().setHSL(0.25 + hash(i, 227) * 0.07, 0.42, 0.17 + hash(i, 229) * 0.08));
      } else if (nt < 1600) {
        const k = 0.7 + hash(i, 231) * 0.8;
        tufts.setMatrixAt(nt, m.compose(t.set(x, y - 0.02, z), q.setFromEuler(new THREE.Euler(0, hash(i, 233) * 6, 0)), s.setScalar(k)));
        tufts.setColorAt(nt++, new THREE.Color().setHSL(0.2 + hash(i, 237) * 0.08, 0.45, 0.26 + hash(i, 239) * 0.12));
      }
    }
    bushes.count = nb;
    tufts.count = nt;
    bushes.castShadow = bushes.receiveShadow = tufts.receiveShadow = true;
    g.add(bushes, tufts);
    return g;
  }

  // Banks of low cloud resting on the hills up the valley, as most mornings in
  // the Andes. Soft sprites, drifting slowly; the fog fades them with distance.
  private makeMist(): THREE.Sprite[] {
    const c = document.createElement("canvas");
    c.width = c.height = 256;
    const g = c.getContext("2d")!;
    for (let k = 0; k < 18; k++) {
      const x = 60 + hash(k, 301) * 136, y = 90 + hash(k, 303) * 76, r = 40 + hash(k, 307) * 60;
      const grad = g.createRadialGradient(x, y, 0, x, y, r);
      grad.addColorStop(0, "rgba(255,255,255,0.35)");
      grad.addColorStop(1, "rgba(255,255,255,0)");
      g.fillStyle = grad;
      g.fillRect(0, 0, 256, 256);
    }
    const tex = new THREE.CanvasTexture(c);
    tex.colorSpace = THREE.SRGBColorSpace;
    return Array.from({ length: 9 }, (_, i) => {
      const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, opacity: 0.28, color: 0xeef3f5 }));
      const z = -45 - i * 16 - hash(i, 311) * 10, x = centre(z) + (hash(i, 313) - 0.5) * 70;
      sp.position.set(x, groundHeight(x, z) + 4 + hash(i, 317) * 6, z);
      sp.scale.set(46 + hash(i, 319) * 30, 16 + hash(i, 321) * 8, 1);
      sp.userData.x = x;
      return sp;
    });
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
    this.walk(state, dt);
    this.water.position.y = state.levelM;
    // The stream flows toward the camera; higher water flows faster.
    this.waves.offset.y += dt * (0.05 + state.levelM * 0.08);
    this.waterTime.value += dt * (0.4 + state.levelM * 0.6);
    // White water at the banks; a mudflow's froth is thin and brown.
    this.foam.value = state.debrisFlow ? 0.15 : 0.7 - state.mud * 0.35;
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
    const t = performance.now() / 1000;
    for (const [i, m] of this.mist.entries()) {
      m.material.opacity = (0.28 + r * 0.25) * (1 - n * 0.8);
      m.position.x = m.userData.x + Math.sin(t * 0.02 + i) * 6;
    }

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

  // People pace a stretch of bank close to the water, watching it. They keep
  // their feet dry: as it rises they step back up the bank, and stay close to
  // the edge, which is what puts them at risk.
  private walk(state: WorldState, dt: number) {
    // Water at the path: they step back to the trail, still watching, still
    // close. Only the order to evacuate sends them to the top.
    const wet = state.levelM > PATH_H - 0.1;
    this.people.forEach((p, i) => {
      p.visible = i < state.people;
      if (!p.visible) return;
      const b = this.peopleBase[i], w = this.walkers[i], speed = WALKERS[i].speed;
      const pace = () => {
        const u = w.u;
        return { d: Math.abs(b.dx), z: b.z - PACE_M / 2 + (u < 0.5 ? u : 1 - u) * 2 * PACE_M };
      };
      // The way out: along the path to the foot of the trail, up it, to the landing.
      const route = [w.e > 0 ? w.from : pace(), TRAIL.foot, TRAIL.top, { d: TRAIL.landing.d, z: TRAIL.landing.z - i * 0.9 }];
      const lengths = route.slice(1).map((q, k) => Math.hypot(q.d - route[k].d, q.z - route[k].z));
      const total = lengths.reduce((x, y) => x + y, 0);
      const goal = state.evacuate ? total : wet ? lengths[0] + lengths[1] * 0.45 : 0;
      let rate = 1; // the walk's playback speed
      let at: { d: number; z: number };
      if (goal > 0 || w.e > 0) {
        if (w.e === 0) w.from = pace();
        const up = goal > w.e, v = up ? HURRY : speed;
        w.e = Math.abs(goal - w.e) <= v * dt ? goal : w.e + (up ? v : -v) * dt;
        rate = w.e === goal && goal > 0 ? 0 : v / speed; // standing, watching the water
        let e = w.e, k = 0;
        while (k < lengths.length - 1 && e > lengths[k]) e -= lengths[k++];
        const f = lengths[k] ? e / lengths[k] : 0;
        at = { d: route[k].d + (route[k + 1].d - route[k].d) * f, z: route[k].z + (route[k + 1].z - route[k].z) * f };
      } else {
        w.u = (w.u + (dt * speed) / (2 * PACE_M)) % 1;
        at = pace();
      }
      const x = centre(at.z) + b.out * at.d;
      const prev = p.position.clone();
      p.position.set(x, groundHeight(x, at.z), at.z);
      // Face where they walk; at the top, face the water. Turn, not snap.
      const mx = p.position.x - prev.x, mz = p.position.z - prev.z;
      const yaw = rate === 0 ? -b.out * Math.PI / 2 : Math.hypot(mx, mz) > 1e-4 ? Math.atan2(mx, mz) : p.rotation.y;
      const turn = Math.atan2(Math.sin(yaw - p.rotation.y), Math.cos(yaw - p.rotation.y));
      p.rotation.y += turn * Math.min(1, dt * 6);
      if (this.mixers[i]) this.mixers[i]!.timeScale = Math.min(rate, 2.6);
      this.mixers[i]?.update(dt);
    });
  }

  private updateProps(state: WorldState, dt: number) {
    this.dam.visible = state.dam;
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

// A pine, about 9 m: a straight trunk and tiers of drooping, ragged boughs,
// darker underneath. Seed 1 is narrower, like the stands planted on the slopes.
function pineGeometry(seed: number): THREE.BufferGeometry {
  const parts: THREE.BufferGeometry[] = [];
  const paint = (geo: THREE.BufferGeometry, color: (y: number) => THREE.Color) => {
    const p = geo.attributes.position as THREE.BufferAttribute;
    const col = new Float32Array(p.count * 3);
    for (let i = 0; i < p.count; i++) {
      const c = color(p.getY(i));
      col.set([c.r, c.g, c.b], i * 3);
    }
    geo.setAttribute("color", new THREE.BufferAttribute(col, 3));
    geo.deleteAttribute("uv");
    return geo;
  };
  const trunk = new THREE.CylinderGeometry(0.1, 0.24, 9, 6).translate(0, 4.5, 0);
  parts.push(paint(trunk, () => new THREE.Color(0x4a3626)));
  const tiers = seed === 1 ? 7 : 6, width = seed === 1 ? 1.45 : 2.1;
  for (let k = 0; k < tiers; k++) {
    const r = width * (1 - k / (tiers + 1.2)), h = 2.3 - k * 0.12, y0 = 2.4 + k * (6.4 / tiers);
    const cone = new THREE.ConeGeometry(r, h, 8, 2).toNonIndexed();
    const p = cone.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const x = p.getX(i), y = p.getY(i), z = p.getZ(i);
      const j = 0.82 + noise(x * 3 + k * 7 + seed * 13, z * 3 + y) * 0.36;
      p.setXYZ(i, x * j, y - (y < 0 ? 0.25 * Math.hypot(x, z) / r : 0), z * j); // boughs droop at the tips
    }
    cone.translate(0, y0 + h / 2, 0);
    const base = new THREE.Color(seed === 1 ? 0x1f3d25 : 0x26452a);
    parts.push(paint(cone, (y) => base.clone().multiplyScalar(0.7 + THREE.MathUtils.clamp((y - y0) / h, 0, 1) * 0.55)));
  }
  const geo = mergeGeometries(parts.map((g) => g.index ? g.toNonIndexed() : g))!;
  geo.computeVertexNormals();
  return geo;
}

// A broadleaf tree, about 6 m: a short trunk and a crown of a few lumpy masses.
function broadleafGeometry(seed: number): THREE.BufferGeometry {
  const parts: THREE.BufferGeometry[] = [];
  const colour = (geo: THREE.BufferGeometry, c: THREE.Color, shade: (y: number) => number) => {
    const p = geo.attributes.position as THREE.BufferAttribute;
    const col = new Float32Array(p.count * 3);
    for (let i = 0; i < p.count; i++) {
      const k = c.clone().multiplyScalar(shade(p.getY(i)));
      col.set([k.r, k.g, k.b], i * 3);
    }
    geo.setAttribute("color", new THREE.BufferAttribute(col, 3));
    geo.deleteAttribute("uv");
    return geo.index ? geo.toNonIndexed() : geo;
  };
  parts.push(colour(new THREE.CylinderGeometry(0.14, 0.26, 3.6, 6).translate(0, 1.8, 0), new THREE.Color(0x4f3b2a), () => 1));
  for (let k = 0; k < 4; k++) {
    const r = 1.3 + hash(k, seed + 401) * 0.6;
    const blob = new THREE.IcosahedronGeometry(r, 1);
    const p = blob.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const v = new THREE.Vector3().fromBufferAttribute(p, i);
      v.multiplyScalar(0.8 + noise(v.x * 1.6 + k * 5, v.y * 1.6 + v.z * 1.4 + seed) * 0.4);
      p.setXYZ(i, v.x, v.y * 0.8, v.z);
    }
    const a = (k / 4) * Math.PI * 2 + seed, y = 4.2 + (k % 2) * 0.9;
    blob.translate(Math.cos(a) * (k ? 1.1 : 0), y, Math.sin(a) * (k ? 1.1 : 0));
    parts.push(colour(blob, new THREE.Color(0x3f6b2c), (yy) => 0.65 + THREE.MathUtils.clamp((yy - y + r) / (2 * r), 0, 1) * 0.6));
  }
  const geo = mergeGeometries(parts)!;
  geo.computeVertexNormals();
  return geo;
}

// Foam on the water: white water where the stream meets its banks, and streaks
// carried by the current. Kept away from the gauge, where SAM 3.1 reads the
// waterline: foam there is still water, but should not change what it reads.
function foamShader(sh: THREE.WebGLProgramParametersWithUniforms, time: { value: number }, strength: { value: number }) {
  sh.uniforms.uTime = time;
  sh.uniforms.uFoam = strength;
  sh.uniforms.uGauge = { value: new THREE.Vector2(GAUGE_POS.x, GAUGE_POS.z) };
  sh.vertexShader = sh.vertexShader
    .replace("#include <common>", "#include <common>\nvarying vec3 vWorld;")
    .replace("#include <begin_vertex>", "#include <begin_vertex>\nvWorld = (modelMatrix * vec4(transformed, 1.0)).xyz;");
  sh.fragmentShader = sh.fragmentShader
    .replace("#include <common>", `#include <common>
varying vec3 vWorld;
uniform float uTime;
uniform float uFoam;
uniform vec2 uGauge;
float fh(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
float fn(vec2 p) {
  vec2 i = floor(p), f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(fh(i), fh(i + vec2(1, 0)), u.x), mix(fh(i + vec2(0, 1)), fh(i + vec2(1, 1)), u.x), u.y);
}`)
    .replace("#include <color_fragment>", `#include <color_fragment>
{
  float c = 2.2 * sin(vWorld.z * 0.045) + 0.8 * sin(vWorld.z * 0.11 + 1.0);
  float d = abs(vWorld.x - c);
  vec2 q = vec2(vWorld.x * 1.7, vWorld.z * 0.45 - uTime); // minus: carried toward +z, downstream, like the logs
  float n = fn(q) * 0.6 + fn(q * 2.3 + 7.0) * 0.4;
  float bank = smoothstep(${(BED_HALF_WIDTH - 1.1).toFixed(2)}, ${(BED_HALF_WIDTH + 0.4).toFixed(2)}, d) * smoothstep(0.35, 0.7, n);
  float streak = smoothstep(0.78, 0.92, fn(vec2(vWorld.x * 3.0, vWorld.z * 0.25 - uTime * 1.3))) * 0.5;
  float away = smoothstep(2.5, 5.0, distance(vWorld.xz, uGauge));
  float foam = clamp(bank + streak, 0.0, 1.0) * away * uFoam;
  diffuseColor.rgb = mix(diffuseColor.rgb, vec3(0.86, 0.89, 0.88), foam);
}`);
}
