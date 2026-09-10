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
    filterBookings();
  });

  // Keep the controls outside the swapped list so an autosave cannot replace
  // a search keystroke. Reapply their current state to every fresh fragment.
  function filterBookings() {
    var input = document.getElementById("booking-search");
    var toggle = document.getElementById("booking-active-filter");
    if (!input || !toggle) return;
    var activeOnly = toggle.checked;
    var query = input.value.trim().toLowerCase();
    var searching = Array.from(query).length >= 2;
    var terms = query.split(/\s+/);
    var count = 0;
    var suggestions = new Set();
    document.querySelectorAll("[data-booking-name]").forEach(function (row) {
      var text = row.getAttribute("data-booking-search");
      var active = !activeOnly || row.getAttribute("data-booking-active") === "true";
      var matches = !searching || terms.every(function (term) { return text.includes(term); });
      row.hidden = !active || !matches;
      if (!row.hidden) count++;
      if (active && searching && matches) {
        JSON.parse(row.getAttribute("data-booking-suggestions")).forEach(function (value) {
          if (terms.every(function (term) { return value.toLowerCase().includes(term); })) {
            suggestions.add(value);
          }
        });
      }
    });
    var empty = document.querySelector("[data-booking-no-matches]");
    if (empty) empty.hidden = count !== 0;
    var list = document.getElementById("booking-suggestions");
    list.replaceChildren();
    Array.from(suggestions).sort().slice(0, 20).forEach(function (name) {
      var option = document.createElement("option");
      option.value = name;
      list.appendChild(option);
    });
    document.querySelector("[data-booking-clear]").disabled = input.value === "";

    function withFilters(href) {
      var url = new URL(href, location.origin);
      if (input.value) url.searchParams.set("q", input.value);
      else url.searchParams.delete("q");
      if (activeOnly) url.searchParams.delete("all");
      else url.searchParams.set("all", "true");
      return url.pathname + url.search;
    }
    history.replaceState(history.state, "", withFilters(location.href));
    document.querySelectorAll('a[href^="/bookings?"]').forEach(function (link) {
      link.setAttribute("href", withFilters(link.href));
    });
  }

  document.addEventListener("input", function (e) {
    if (e.target.id === "booking-search") filterBookings();
  });

  document.addEventListener("change", function (e) {
    if (e.target.id === "booking-active-filter") filterBookings();
  });

  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-booking-clear]")) {
      var input = document.getElementById("booking-search");
      input.value = "";
      filterBookings();
      input.focus();
    }
  });

  document.addEventListener("htmx:afterSwap", function (e) {
    if (e.target && e.target.id === "booking-list") filterBookings();
  });

  function currentDialog() {
    return document.querySelector("dialog[data-dialog]");
  }

  // A draft nobody typed into is worth nothing, so closing its dialog throws
  // it away again. The flag is set from the input events rather than from the
  // save requests, because a bare Escape also produces a keyup and would
  // otherwise count as an edit.
  var dialogTouched = false;
  var saveErrors = new Map();

  // A click on the backdrop reports the dialog itself as its target, because
  // everything visible sits in a child element.
  function isBackdrop(el) {
    return el && el.tagName === "DIALOG" && el.hasAttribute("data-dialog");
  }

  // Selecting text inside the dialog and releasing outside of it must not
  // count as a click on the backdrop, so the press has to start there too.
  var pressedBackdrop = false;

  document.addEventListener("mousedown", function (e) {
    pressedBackdrop = isBackdrop(e.target);
  });

  document.addEventListener("click", function (e) {
    if (pressedBackdrop && isBackdrop(e.target)) {
      e.target.close();
      return;
    }

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
    dialogTouched = false;
    saveErrors.clear();
    refreshSplitState();
    // A modal focuses its first field anyway, so a suggested name is selected
    // rather than cleared: typing replaces it, clicking into the field wipes it.
    var name = dlg.querySelector("input[data-clear-on-focus]");
    if (name) name.select();
  });

  // Typing into a field beats what the server knows: the save may still be in
  // flight when the dialog closes, so a draft that was typed into is never
  // discarded. Picking from a select or a checkbox carries no value of its own
  // and leaves the decision to the server.
  function markDialogTouched(e) {
    var el = e.target;
    if (!el || (el.tagName !== "INPUT" && el.tagName !== "TEXTAREA")) return;
    if (el.type === "checkbox" || el.type === "radio") return;
    var dlg = currentDialog();
    if (dlg && dlg.contains(el)) dialogTouched = true;
  }

  document.addEventListener("input", markDialogTouched);

  // While a booking is still a draft, its name proposes the category: the
  // longest option of the list that appears in the name wins. Typing into the
  // category field yourself ends the guessing.
  document.addEventListener("input", function (e) {
    var el = e.target;
    if (!el || !el.name) return;
    if (el.hasAttribute("data-category")) {
      el.removeAttribute("data-suggest");
      return;
    }
    if (el.name !== "name") return;

    var dlg = currentDialog();
    if (!dlg) return;
    var cat = dlg.querySelector("input[data-category][data-suggest]");
    if (!cat) return;

    var typed = el.value.toLowerCase();
    var best = "";
    dlg.querySelectorAll("datalist option").forEach(function (o) {
      var name = o.value.toLowerCase();
      if (typed.indexOf(name) !== -1 && o.value.length > best.length) best = o.value;
    });
    if (best && best !== cat.value) {
      cat.value = best;
    }
  });

  // Closing empties the container, otherwise a stale dialog would linger in
  // the DOM and its fields would keep posting.
  document.addEventListener("close", function (e) {
    if (!e.target || e.target.tagName !== "DIALOG") return;
    saveErrors.clear();
    var discard = dialogTouched ? "" : e.target.getAttribute("data-discard-url");
    var host = document.getElementById("booking-dialog");
    if (host) host.innerHTML = "";
    if (discard) {
      // The answer carries HX-Trigger, so the list refreshes once the draft is
      // actually gone.
      htmx.ajax("POST", discard, { source: document.body, swap: "none" });
      return;
    }
    document.body.dispatchEvent(new CustomEvent("hb:changed"));
  }, true);

  document.body.addEventListener("htmx:afterRequest", function (e) {
    var d = e.detail;
    if (!d) return;
    var dlg = currentDialog();
    if (dlg && d.elt && dlg.contains(d.elt)) {
      var error = dlg.querySelector("[data-save-errors]");
      if (error) {
        var path = d.requestConfig && d.requestConfig.path;
        if (d.successful) {
          saveErrors.delete(path);
        } else {
          var message = d.xhr && d.xhr.responseText.trim();
          saveErrors.set(path, message || error.getAttribute("data-fallback"));
        }
        error.textContent = Array.from(new Set(saveErrors.values())).join("\n");
        error.hidden = saveErrors.size === 0;
        var saved = dlg.querySelector("[data-saved-hint]");
        if (saved) saved.hidden = saveErrors.size !== 0;
      }
    }
    if (!d.successful) return;
    var verb = d.requestConfig && d.requestConfig.verb;
    if (verb && verb.toLowerCase() !== "get" && saveErrors.size === 0) {
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

  // Percentages that do not add up to a hundred leave part of a booking on
  // nobody's tab, and a booking nobody carries at all only shows up as
  // "unassigned" in the list. The dialog counts along while the shares are
  // typed and says which pick is missing.
  function refreshSplitState(e) {
    var dlg = currentDialog();
    if (!dlg) return;
    var picker = dlg.querySelector('select[name="split_mode"]');
    var mode = picker ? picker.value : "equal";
    var sum = 0;
    var carriers = 0;
    var carrier = "";
    var fixedCents = 0;
    var sharesValid = true;

    dlg.querySelectorAll(".split-member").forEach(function (row) {
      var on = row.querySelector('input[type="checkbox"]');
      if (!on || !on.checked) return;
      var percentField = row.querySelector("input[data-percent]");
      var fixedField = row.querySelector("input[data-cents]");
      var percent = percentField ? parseFloat(percentField.value) || 0 : 0;
      var fixed = fixedField ? parseFloat(fixedField.value) || 0 : 0;
      var activeField = mode === "percent" ? percentField : mode === "fixed" ? fixedField : null;
      if (activeField && !activeField.validity.valid) sharesValid = false;
      sum += percent;
      fixedCents += Math.round(fixed * 100);
      // Outside an equal split a tick without a value carries nothing.
      if (mode === "equal" || (mode === "percent" ? percent > 0 : fixed > 0)) {
        carriers++;
        carrier = on.name.slice(2);
      }
    });

    var total = dlg.querySelector("[data-split-total]");
    if (total) total.textContent = String(Math.round(sum * 10) / 10);
    var hint = dlg.querySelector("[data-split-hint]");
    if (hint) hint.classList.toggle("split-off", Math.abs(sum - 100) > 0.05);
    var warn = dlg.querySelector("[data-carrier-warn]");
    if (warn) warn.classList.toggle("carrier-warn-on", carriers === 0);

    var settle = dlg.querySelector("[data-settle-toggle]");
    var preference = dlg.querySelector("[data-settle-preference]");
    if (!settle || !preference) return;
    if (e && e.target === settle && !settle.disabled) {
      preference.value = settle.checked ? "1" : "";
    }
    var payer = dlg.querySelector('input[name="payer_member_id"]:checked');
    var complete = sharesValid && (mode === "equal" || (mode === "percent" ?
      Math.abs(sum - 100) < 1e-9 : fixedCents === settlementAmount(dlg)));
    var automatic = complete && carriers === 1 && payer && payer.value === carrier;
    settle.disabled = Boolean(automatic);
    settle.checked = !automatic && preference.value === "1";
    var settleHint = dlg.querySelector("[data-settle-hint]");
    if (settleHint) {
      settleHint.textContent = settleHint.getAttribute(automatic ? "data-auto" : "data-default");
    }
  }

  function settlementAmount(dlg) {
    var field = dlg.querySelector('input[name="amount"]');
    var amount = field.validity.valid ? Math.round(Number(field.value) * 100) : NaN;
    var recurring = dlg.querySelector('input[name="recurring"]');
    if (!recurring || !recurring.checked) return amount;
    var month = dlg.getAttribute("data-month");
    dlg.querySelectorAll(".override-row:not(.override-add)").forEach(function (row) {
      var from = row.querySelector('input[name="starts_on"]');
      var until = row.querySelector('input[name="ends_on"]');
      var value = row.querySelector('input[name="amount"]');
      if (from.validity.badInput || until.validity.badInput || value.validity.badInput) {
        amount = NaN;
      } else if ((!from.value || from.value.slice(0, 7) <= month) &&
                 (!until.value || until.value.slice(0, 7) >= month)) {
        amount = Math.round(Number(value.value) * 100);
      }
    });
    return amount;
  }

  // Update the saved preference before HTMX collects the form on change.
  document.addEventListener("input", refreshSplitState, true);
  document.addEventListener("change", refreshSplitState, true);

  // A period picker carries the whole target URL in each option, so following
  // it needs no knowledge of the page. The handler lives here because the
  // content security policy forbids the inline one a select would otherwise use.
  document.addEventListener("change", function (e) {
    var el = e.target;
    if (el && el.tagName === "SELECT" && el.hasAttribute("data-nav") && el.value) {
      window.location.assign(el.value);
    }
  });
})();
