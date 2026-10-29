import * as THREE from "three";
import { World, GAUGE_POS } from "./world";
import { Lens, type LensState } from "./lens";

// The camera node: a fixed camera on a pole, framing the gauge and the
// stream. It renders into its own canvas at the resolution a real node would
// send, passes the frame through the lens (raindrops, haze), and posts JPEG
// frames to the station service.

export const FRAME_W = 1280;
export const FRAME_H = 720;

export class CameraNode {
  readonly canvas: HTMLCanvasElement; // the 3D render
  readonly output: HTMLCanvasElement; // what the camera actually sends: render + lens
  readonly camera = new THREE.PerspectiveCamera(34, FRAME_W / FRAME_H, 0.1, 400);
  readonly lensState: LensState = { drops: false, haze: 0, ir: 0 };
  shake = 0; // 0 still .. 1 strong wind shaking the pole
  infrared = true; // night vision with its own lamp, as most outdoor IP cameras have
  private renderer: THREE.WebGLRenderer;
  private lens = new Lens(FRAME_W, FRAME_H);
  private out: CanvasRenderingContext2D;
  private home = new THREE.Quaternion(); // where the camera points when nobody touched it
  private knocked = new THREE.Quaternion(); // a knock leaves it pointing elsewhere
  private lastAt = performance.now();
  // The infrared lamp next to the lens. It is on only while the node renders:
  // people do not see infrared, so the main view stays dark at night.
  private lamp = new THREE.SpotLight(0xffffff, 0, 40, THREE.MathUtils.degToRad(32), 0.6, 1.6);

  constructor(private world: World) {
    this.canvas = document.createElement("canvas");
    this.renderer = new THREE.WebGLRenderer({ canvas: this.canvas, antialias: true, preserveDrawingBuffer: true });
    this.renderer.setPixelRatio(1);
    this.renderer.setSize(FRAME_W, FRAME_H, false);
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    this.renderer.shadowMap.enabled = true;
    this.renderer.shadowMap.autoUpdate = false; // static casters: one shadow pass
    this.renderer.shadowMap.needsUpdate = true;
    // Mounted on a pole well above the highest flood the twin can produce (3.6 m):
    // a camera under water sees nothing, in the twin and in the field.
    this.camera.position.set(GAUGE_POS.x - 6.5, 6.5, GAUGE_POS.z + 11);
    this.camera.lookAt(GAUGE_POS.x - 2.2, 1.4, GAUGE_POS.z - 3);
    this.home.copy(this.camera.quaternion);
    this.lamp.position.copy(this.camera.position);
    this.lamp.target.position.set(GAUGE_POS.x - 2.2, 1.4, GAUGE_POS.z - 3);
    world.scene.add(this.lamp, this.lamp.target);
    this.knocked.identity();
    this.env = world.reflection(this.renderer);
    this.output = document.createElement("canvas");
    this.output.width = FRAME_W;
    this.output.height = FRAME_H;
    this.out = this.output.getContext("2d")!;
  }

  private env: THREE.Texture;

  get isKnocked() {
    return !this.knocked.equals(new THREE.Quaternion());
  }

  // knock tilts the camera a few degrees and leaves it there, as a branch or a
  // bump would: the calibration no longer matches what the camera sees.
  knock() {
    this.knocked.setFromEuler(new THREE.Euler(THREE.MathUtils.degToRad(-4 - Math.random() * 2), THREE.MathUtils.degToRad(3 + Math.random() * 2), 0));
  }

  fix() {
    this.knocked.identity();
  }

  render() {
    const now = performance.now(), dt = Math.min(0.5, (now - this.lastAt) / 1000);
    this.lastAt = now;
    // The pole sways in the wind: a few tenths of a degree, quickly.
    const t = now / 1000, a = THREE.MathUtils.degToRad(0.9) * this.shake;
    const sway = new THREE.Quaternion().setFromEuler(new THREE.Euler(Math.sin(t * 13.1) * a, Math.sin(t * 9.7 + 1) * a, Math.sin(t * 7.3) * a * 0.5));
    this.camera.quaternion.copy(this.home).multiply(this.knocked).multiply(sway);
    // Without infrared a camera at night sees almost nothing; with it, the
    // lamp lights the scene and the picture turns monochrome.
    const ir = this.infrared ? THREE.MathUtils.smoothstep(this.world.night, 0.3, 0.7) : 0;
    this.lensState.ir = ir;
    this.renderer.toneMappingExposure = ir > 0 ? Math.max(this.world.exposure, 0.85 * ir + this.world.exposure * (1 - ir)) : this.world.exposure ** 3;
    this.world.setWaterEnvironment(this.env);
    this.lamp.intensity = 260 * ir;
    this.renderer.render(this.world.scene, this.camera);
    this.lamp.intensity = 0;
    this.lens.apply(this.out, this.canvas, this.lensState, dt);
  }

  jpeg(quality = 0.85): Promise<Blob> {
    this.render();
    return new Promise((ok, fail) => this.output.toBlob((b) => (b ? ok(b) : fail(new Error("no frame"))), "image/jpeg", quality));
  }

  // Frame pixel of a world point, as the service sees it (y down), with the
  // camera where it was installed: the calibration is made once, at install.
  project(p: THREE.Vector3): { x: number; y: number } {
    const q = this.camera.quaternion.clone();
    this.camera.quaternion.copy(this.home);
    this.camera.updateMatrixWorld(); // before the first render the view matrix is still identity
    const v = p.clone().project(this.camera);
    this.camera.quaternion.copy(q);
    this.camera.updateMatrixWorld();
    return { x: ((v.x + 1) / 2) * FRAME_W, y: ((1 - v.y) / 2) * FRAME_H };
  }
}
