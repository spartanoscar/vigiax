// What happens to a real camera in the field, drawn over the camera node's
// frame before it is sent: raindrops on the lens, and fog or dirt over it. The
// shake and the knock move the camera itself (camera-node.ts).

export interface LensState {
  drops: boolean; // raindrops sitting on and sliding down the lens
  haze: number; // 0 clean .. 1 fogged or covered in dirt
  ir: number; // 0 colour .. 1 infrared night vision (monochrome, lit near the camera)
}

type Drop = { x: number; y: number; r: number; v: number };

export class Lens {
  private drops: Drop[] = [];

  constructor(private w: number, private h: number) {
    for (let i = 0; i < 38; i++) this.drops.push(this.drop(Math.random() * h));
  }

  private drop(y: number): Drop {
    return { x: Math.random() * this.w, y, r: 6 + Math.random() ** 2 * 26, v: Math.random() < 0.3 ? 20 + Math.random() * 60 : 0 };
  }

  // apply draws src through the lens into ctx (same size).
  apply(ctx: CanvasRenderingContext2D, src: HTMLCanvasElement, s: LensState, dt: number) {
    if (s.ir > 0.01) {
      ctx.filter = `grayscale(${s.ir}) contrast(${1 + 0.25 * s.ir}) brightness(${1 + 0.15 * s.ir})`;
      ctx.drawImage(src, 0, 0);
      ctx.filter = "none";
      // The infrared lamp lights what is near and in front; the edges fall off.
      const g = ctx.createRadialGradient(this.w * 0.55, this.h * 0.62, this.h * 0.25, this.w * 0.55, this.h * 0.62, this.w * 0.75);
      g.addColorStop(0, "rgba(0,0,0,0)");
      g.addColorStop(1, `rgba(0,0,0,${0.75 * s.ir})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, this.w, this.h);
    } else {
      ctx.drawImage(src, 0, 0);
    }
    if (s.drops) {
      for (const d of this.drops) {
        d.y += d.v * dt;
        if (d.y - d.r > this.h) Object.assign(d, this.drop(-d.r));
        // A drop is a small lens: it shows a blurred, flipped patch of what is
        // above it, which is why water on the glass hides what is behind it.
        ctx.save();
        ctx.beginPath();
        ctx.ellipse(d.x, d.y, d.r, d.r * 1.15, 0, 0, Math.PI * 2);
        ctx.clip();
        ctx.filter = "blur(2px)";
        ctx.translate(d.x, d.y);
        ctx.scale(1, -1);
        ctx.drawImage(src, d.x - d.r * 2, d.y - d.r * 4, d.r * 4, d.r * 4, -d.r * 1.2, -d.r * 1.2, d.r * 2.4, d.r * 2.4);
        ctx.restore();
        const g = ctx.createRadialGradient(d.x - d.r * 0.3, d.y - d.r * 0.4, 1, d.x, d.y, d.r * 1.1);
        g.addColorStop(0, "rgba(255,255,255,0.35)");
        g.addColorStop(0.6, "rgba(255,255,255,0.03)");
        g.addColorStop(1, "rgba(0,0,0,0.18)");
        ctx.fillStyle = g;
        ctx.beginPath();
        ctx.ellipse(d.x, d.y, d.r, d.r * 1.15, 0, 0, Math.PI * 2);
        ctx.fill();
      }
    }
    if (s.haze > 0.01) {
      // Fog on the glass: everything washes out toward a milky grey, more at the edges.
      const g = ctx.createRadialGradient(this.w / 2, this.h / 2, this.h * 0.2, this.w / 2, this.h / 2, this.w * 0.7);
      g.addColorStop(0, `rgba(205,210,206,${0.55 * s.haze})`);
      g.addColorStop(1, `rgba(190,190,180,${0.95 * s.haze})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, this.w, this.h);
    }
  }
}
