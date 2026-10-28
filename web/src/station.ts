// Each visitor gets an own station, so two people raising the water at once
// don't mix their readings. The landing and the twin share it through
// localStorage, so the WhatsApp sign-up on the landing matches the twin. A
// link from WhatsApp (?station=twin-ab12cd) opens the station the phone signed
// up for.
export const STATION = (() => {
  const fresh = () => "twin-" + Array.from(crypto.getRandomValues(new Uint8Array(6)), (b) => (b % 36).toString(36)).join("");
  const linked = new URLSearchParams(location.search).get("station")?.toLowerCase();
  const fromLink = linked && /^twin-[a-z0-9]{6}$/.test(linked) ? linked : null;
  try {
    const id = fromLink ?? localStorage.getItem("vigiax-station") ?? fresh();
    localStorage.setItem("vigiax-station", id);
    return id;
  } catch {
    return fromLink ?? fresh();
  }
})();
