import "@fontsource-variable/inter";
import "@fontsource-variable/outfit";
import "./landing.css";
import { STATION } from "./station";

// The landing needs very little script: the nav turns solid once the hero
// scrolls away, and blocks below the fold fade in as they arrive. Nothing
// above the fold waits for script to be visible.

const nav = document.getElementById("nav")!;
const onScroll = () => nav.classList.toggle("scrolled", scrollY > 24);
addEventListener("scroll", onScroll, { passive: true });
onScroll();

if ("IntersectionObserver" in window && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
  const targets = document.querySelectorAll<HTMLElement>(
    ".section .h2, .section .body-lg, .stat-cards, .quote, .step, .levels, .band-visual, .checks, .model, .faq-list, .final-in",
  );
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        const el = e.target as HTMLElement;
        el.classList.add("in");
        io.unobserve(el);
        setTimeout(() => (el.style.transitionDelay = ""), 1200); // hover effects stay instant
      }
    },
    { rootMargin: "0px 0px -8% 0px" },
  );
  for (const el of targets) {
    if (el.getBoundingClientRect().top < innerHeight) continue; // already on screen: leave it alone
    el.classList.add("reveal");
    io.observe(el);
  }
  // Steps and model cards arrive one after another.
  document.querySelectorAll<HTMLElement>(".step, .model").forEach((el, i) => (el.style.transitionDelay = `${(i % 5) * 70}ms`));
  document.documentElement.classList.add("js");
}

// WhatsApp sign-up for this visitor's own twin station: the same id the twin
// uses, so the alert that arrives is the flood this visitor triggers.
const WA = "573001128564";
const signup = `ALERTS ${STATION}`;
const link = `https://wa.me/${WA}?text=${encodeURIComponent(signup)}`;
for (const id of ["wa-qr", "wa-open"]) (document.getElementById(id) as HTMLAnchorElement).href = link;
document.getElementById("wa-code")!.textContent = signup;
// The QR only matters where the page is not already on a phone; load its
// generator only there.
if (matchMedia("(min-width: 721px) and (hover: hover)").matches) {
  import("qrcode-generator").then(({ default: qrcode }) => {
    const qr = qrcode(0, "M");
    qr.addData(link);
    qr.make();
    document.getElementById("wa-qr")!.innerHTML = qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true });
  });
}

// Real river: slide between the USGS photo and the water SAM 3.1 found on it,
// on a low-water day or at the flood peak.
const DAYS: Record<string, { label: string; gage: string; sam: string; alt: string }> = {
  before: { label: "Oct 13", gage: "0.93", sam: "0.92", alt: "on October 13, low water" },
  peak: { label: "Oct 15, flood peak", gage: "4.56", sam: "4.54", alt: "on October 15 at the flood peak, the river filling the frame" },
};
const view = document.querySelector<HTMLElement>(".cmp-view");
if (view) {
  const range = view.querySelector("input")!;
  const photo = view.querySelector<HTMLImageElement>(".cmp-photo")!;
  const sam = view.querySelector<HTMLImageElement>(".cmp-sam img")!;
  const cap = view.closest("figure")!.querySelector("figcaption")!;
  const set = (v: string | number) => view.style.setProperty("--pos", `${v}%`);
  range.addEventListener("input", () => {
    view.classList.remove("intro");
    set(range.value);
  });
  document.querySelectorAll<HTMLButtonElement>("[data-day]").forEach((b) =>
    b.addEventListener("click", () => {
      const d = DAYS[b.dataset.day!];
      document.querySelectorAll("[data-day]").forEach((x) => x.setAttribute("aria-selected", String(x === b)));
      photo.src = `/img/el-reno-${b.dataset.day}-photo.webp`;
      sam.src = `/img/el-reno-${b.dataset.day}.webp`;
      photo.alt = `USGS webcam on the North Canadian River at El Reno ${d.alt}.`;
      cap.innerHTML = `<strong>${d.label}</strong> · official gage ${d.gage}&nbsp;m · SAM&nbsp;3.1 ${d.sam}&nbsp;m`;
    }),
  );
  // The other day's pair loads ahead, so switching never flashes empty.
  const warm = () => ["peak-photo", "peak"].forEach((n) => (new Image().src = `/img/el-reno-${n}.webp`));
  // On arrival the SAM layer sweeps in from the right and settles on the river.
  if ("IntersectionObserver" in window && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    set(96);
    const io = new IntersectionObserver(([e]) => {
      if (!e.isIntersecting) return;
      io.disconnect();
      warm();
      view.classList.add("intro");
      setTimeout(() => set(range.value), 250);
      view.addEventListener("transitionend", () => view.classList.remove("intro"), { once: true });
    }, { rootMargin: "0px 0px -20% 0px" });
    io.observe(view);
  } else warm();
}
