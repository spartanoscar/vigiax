// The stream's sound: a calm loop and a flood loop (ASSETS.md), crossfaded by
// how high and how muddy the water is. Browsers only play sound after a click
// or a key, so it starts on the first one; the speaker button mutes it, and the
// choice is remembered.
import type { WorldState } from "./world";

const KEY = "vigiax-sound";
const $ = (id: string) => document.getElementById(id) as HTMLElement;

class Ambience {
  private ctx: AudioContext | null = null;
  private calm: GainNode | null = null;
  private flood: GainNode | null = null;
  private master: GainNode | null = null;
  private mix = -1;
  on = (() => { try { return localStorage.getItem(KEY) !== "0"; } catch { return true; } })();

  constructor() {
    const btn = $("sound-toggle");
    const show = () => {
      btn.setAttribute("aria-pressed", String(this.on));
      btn.title = this.on ? "Mute the stream" : "Play the stream's sound";
      btn.classList.toggle("muted", !this.on);
    };
    show();
    btn.onclick = (e) => {
      e.stopPropagation();
      this.on = !this.on;
      try { localStorage.setItem(KEY, this.on ? "1" : "0"); } catch {}
      show();
      if (this.on) this.start();
      this.level();
    };
    const first = () => {
      removeEventListener("pointerdown", first);
      removeEventListener("keydown", first);
      if (this.on) this.start();
    };
    addEventListener("pointerdown", first);
    addEventListener("keydown", first);
    document.addEventListener("visibilitychange", () => {
      if (!this.ctx) return;
      if (document.hidden) this.ctx.suspend();
      else this.ctx.resume();
    });
  }

  private async start() {
    if (this.ctx) return void this.ctx.resume();
    try {
      const ctx = (this.ctx = new AudioContext());
      this.master = ctx.createGain();
      this.master.gain.value = 0;
      this.master.connect(ctx.destination);
      const loop = async (url: string) => {
        const buf = await ctx.decodeAudioData(await (await fetch(url)).arrayBuffer());
        const src = ctx.createBufferSource(), gain = ctx.createGain();
        src.buffer = buf;
        src.loop = true;
        // Skip the few silent milliseconds an MP3 encoder pads at each end.
        src.loopStart = 0.05;
        src.loopEnd = buf.duration - 0.05;
        gain.gain.value = 0;
        src.connect(gain).connect(this.master!);
        src.start(0, Math.random() * buf.duration * 0.5);
        return gain;
      };
      [this.calm, this.flood] = await Promise.all([loop("/audio/stream-calm.mp3"), loop("/audio/stream-flood.mp3")]);
      this.mix = -1;
      this.level();
    } catch {
      /* no audio */
    }
  }

  private level() {
    if (!this.ctx || !this.master) return;
    this.master.gain.setTargetAtTime(this.on ? 0.7 : 0, this.ctx.currentTime, 0.4);
  }

  // Called every frame: the flood loop takes over as the water rises from 0.9 m
  // to 2.2 m, and at once in a debris flow.
  update(s: WorldState) {
    if (!this.ctx || !this.calm || !this.flood) return;
    const k = s.debrisFlow ? 1 : Math.min(1, Math.max(0, (s.levelM - 0.9) / 1.3));
    if (Math.abs(k - this.mix) < 0.01) return;
    this.mix = k;
    const t = this.ctx.currentTime;
    this.calm.gain.setTargetAtTime(0.6 * (1 - 0.7 * k), t, 0.8);
    this.flood.gain.setTargetAtTime(k, t, 0.8);
  }
}

export const ambience = new Ambience();
