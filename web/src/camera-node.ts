import * as THREE from "three";
import { World, GAUGE_POS } from "./world";

// The camera node: a fixed camera on a bridge, framing the gauge and the
// stream. It renders into its own canvas at the resolution a real node would
// send, and posts JPEG frames to the station service.

export const FRAME_W = 1280;
export const FRAME_H = 720;

export class CameraNode {
  readonly canvas: HTMLCanvasElement;
  readonly camera = new THREE.PerspectiveCamera(34, FRAME_W / FRAME_H, 0.1, 400);
  private renderer: THREE.WebGLRenderer;

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
    this.env = world.reflection(this.renderer);
  }

  private env: THREE.Texture;

  render() {
    this.world.setWaterEnvironment(this.env);
    this.renderer.render(this.world.scene, this.camera);
  }

  jpeg(quality = 0.85): Promise<Blob> {
    this.render();
    return new Promise((ok, fail) => this.canvas.toBlob((b) => (b ? ok(b) : fail(new Error("no frame"))), "image/jpeg", quality));
  }

  // Frame pixel of a world point, as the service sees it (y down).
  project(p: THREE.Vector3): { x: number; y: number } {
    this.camera.updateMatrixWorld(); // before the first render the view matrix is still identity
    const v = p.clone().project(this.camera);
    return { x: ((v.x + 1) / 2) * FRAME_W, y: ((1 - v.y) / 2) * FRAME_H };
  }
}
