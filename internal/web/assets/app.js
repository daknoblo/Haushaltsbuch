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

  document.body.addEventListener("htmx:afterRequest", function (e) {
    var d = e.detail;
    if (!d || !d.successful) return;
    var verb = d.requestConfig && d.requestConfig.verb;
    if (verb && verb.toLowerCase() !== "get") {
      flashSaved();
    }
  });

  // Full page refresh when the server asks for it (e.g. after switching
  // household), triggered via the HX-Refresh response header handled by htmx.
})();
