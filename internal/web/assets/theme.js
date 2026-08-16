// Applies the persisted theme before the first paint. Loaded in <head> and kept
// separate from app.js so the page never flashes the wrong palette.
(function () {
  "use strict";
  try {
    var stored = localStorage.getItem("hb-theme");
    if (stored === "light" || stored === "dark") {
      document.documentElement.setAttribute("data-theme", stored);
    }
  } catch (e) {
    // Storage can be unavailable (private mode); fall back to the OS setting.
  }
})();
