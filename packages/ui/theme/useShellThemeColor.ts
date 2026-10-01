import { useEffect } from "react";

// While a shell page is open, the browser's toolbar takes the shell's ground (--bg) instead of the public site's
// colour from index.html, and gets it back when the page closes.
export function useShellThemeColor() {
  useEffect(() => {
    const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    const shell = document.querySelector(".app-shell");
    if (!meta || !shell) return;
    const previous = meta.content;
    meta.content = getComputedStyle(shell).getPropertyValue("--bg").trim();
    return () => {
      meta.content = previous;
    };
  }, []);
}
