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

  function currentDialog() {
    return document.querySelector("dialog[data-dialog]");
  }

  document.addEventListener("click", function (e) {
    var toggle = e.target.closest("[data-theme-toggle]");
    if (toggle) {
      var root = document.documentElement;
      var nowDark = root.classList.toggle("dark");
      try {
        localStorage.setItem("hb-theme", nowDark ? "dark" : "light");
      } catch (err) {
        // Ignore storage failures; the choice then lasts for this page only.
      }
      return;
    }

    if (e.target.closest("[data-dialog-close]")) {
      var dlg = currentDialog();
      if (dlg) dlg.close();
      return;
    }

    // A freshly created booking carries a suggested name. Clicking into the
    // field wipes it instead of making the user select and delete it first.
    var suggested = e.target.closest("input[data-clear-on-focus]");
    if (suggested) {
      suggested.removeAttribute("data-clear-on-focus");
      suggested.value = "";
      return;
    }

    var navToggle = e.target.closest("[data-nav-toggle]");
    if (navToggle) {
      var panel = document.querySelector("[data-nav-panel]");
      if (!panel) return;
      var open = panel.hasAttribute("hidden");
      if (open) {
        panel.removeAttribute("hidden");
      } else {
        panel.setAttribute("hidden", "");
      }
      navToggle.setAttribute("aria-expanded", open ? "true" : "false");
    }
  });

  // The booking dialog arrives as a fragment, so it has to be opened here.
  // Doing it in JS rather than an inline handler keeps the CSP free of
  // 'unsafe-eval'.
  document.addEventListener("htmx:afterSwap", function (e) {
    if (!e.target || e.target.id !== "booking-dialog") return;
    var dlg = currentDialog();
    if (!dlg) return;
    if (!dlg.open) dlg.showModal();
    // A modal focuses its first field anyway, so a suggested name is selected
    // rather than cleared: typing replaces it, clicking into the field wipes it.
    var name = dlg.querySelector("input[data-clear-on-focus]");
    if (name) name.select();
  });

  // Closing empties the container, otherwise a stale dialog would linger in
  // the DOM and its fields would keep posting.
  document.addEventListener("close", function (e) {
    if (!e.target || e.target.tagName !== "DIALOG") return;
    var host = document.getElementById("booking-dialog");
    if (host) host.innerHTML = "";
    document.body.dispatchEvent(new CustomEvent("hb:changed"));
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

  // Amounts read as round figures: a trailing ",00" carries no information and
  // only makes the column noisy. Anything the user actually typed is kept.
  document.addEventListener("focusout", function (e) {
    var el = e.target;
    if (el && el.hasAttribute && el.hasAttribute("data-cents")) {
      el.value = el.value.replace(/\.00$/, "");
    }
  });
})();
