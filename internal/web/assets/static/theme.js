// Applies the persisted theme before the first paint. Loaded in <head> and kept
// separate from app.js so the page never flashes the wrong palette. The markup
// ships with class="dark", so only an explicit light choice has to be applied.
(function () {
  "use strict";
  try {
    if (localStorage.getItem("hb-theme") === "light") {
      document.documentElement.classList.remove("dark");
    }
  } catch (e) {
    // Storage can be unavailable (private mode); keep the default palette.
  }
})();
