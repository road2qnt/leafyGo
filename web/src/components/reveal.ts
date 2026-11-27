/**
 * Brings a panel opened from a list into view when the layout has placed it off
 * screen (stacked columns on narrow widths, or a long table).
 */
export function reveal(id: string) {
  requestAnimationFrame(() => {
    const element = document.getElementById(id);
    if (!element) return;
    const { top } = element.getBoundingClientRect();
    if (top < 0 || top > window.innerHeight * 0.6) {
      element.scrollIntoView({ block: "start" });
    }
  });
}
