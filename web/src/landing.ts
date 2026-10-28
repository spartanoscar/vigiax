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
