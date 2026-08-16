// Haushaltsbuch UI helpers.
(function () {
  "use strict";

  // Flash a "saved" indicator after any successful non-GET HTMX request.
  var indicator;
  var hideTimer;

  function flashSaved() {
    if (!indicator) return;
    indicator.classList.add("show");
    clearTimeout(hideTimer);
    hideTimer = setTimeout(function () {
      indicator.classList.remove("show");
    }, 1200);
  }

  document.addEventListener("DOMContentLoaded", function () {
    indicator = document.getElementById("save-indicator");
  });

  // Theme toggle. The initial value is applied by theme.js in <head>.
  function currentTheme() {
    var set = document.documentElement.getAttribute("data-theme");
    if (set) return set;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  document.addEventListener("click", function (e) {
    var toggle = e.target.closest("[data-theme-toggle]");
    if (!toggle) return;
    var next = currentTheme() === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try {
      localStorage.setItem("hb-theme", next);
    } catch (err) {
      // Ignore storage failures; the choice then lasts for this page only.
    }
  });

  // Mirror the open state of an expense row into its form, so the server
  // re-renders the row in the same state after an auto-save.
  document.addEventListener("toggle", function (e) {
    var details = e.target;
    if (!details || details.tagName !== "DETAILS") return;
    var form = details.closest("form");
    if (!form) return;
    var field = form.querySelector("[data-expanded-state]");
    if (field) field.value = details.open ? "1" : "0";
  }, true);

  document.body.addEventListener("htmx:afterRequest", function (e) {
    var d = e.detail;
    if (!d || !d.successful) return;
    var verb = d.requestConfig && d.requestConfig.verb;
    if (verb && verb.toLowerCase() !== "get") {
      flashSaved();
    }
    // Clear "add new entry" forms after a successful submit. Handled here
    // instead of an inline hx-on attribute so the Content-Security-Policy can
    // stay free of 'unsafe-eval'.
    var el = d.elt;
    if (el && el.tagName === "FORM" && el.hasAttribute("data-reset-on-success")) {
      el.reset();
    }
  });

  // Full page refresh when the server asks for it (e.g. after switching
  // household), triggered via the HX-Refresh response header handled by htmx.
})();
