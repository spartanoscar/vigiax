import "@fontsource-variable/inter";
import "@fontsource-variable/outfit";
import "./landing.css";

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
