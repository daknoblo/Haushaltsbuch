(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", function () {
    var form = document.querySelector("[data-bank-upload]");
    if (!form) return;
    var picker = form.querySelector('input[type="file"]');
    var dropzone = form.querySelector("[data-upload-dropzone]");
    var queue = form.querySelector("[data-upload-queue]");
    var queueArea = form.querySelector("[data-upload-queue-area]");
    var template = form.querySelector("[data-upload-item]");
    var message = form.querySelector("[data-upload-message]");
    var runningHint = form.querySelector("[data-bank-progress]");
    var refreshError = form.querySelector("[data-upload-refresh-error]");
    var reviewDeferred = form.querySelector("[data-upload-review-deferred]");
    var labels = form.querySelector("[data-upload-labels]").dataset;
    var entries = [];
    var running = false;
    var paused = false;
    var activeFields = null;
    var refreshing = false;
    var refreshAgain = false;
    var uploadRevision = 0;
    var historyRevision = 0;
    var reviewRevision = 0;
    var followedUpload = false;
    var stale = false;
    var dragDepth = 0;
    var reviewFilter = "all";
    var reviewSelection = new Map();
    var reviewBusy = false;
    var reviewReplaceIDs = new Set();
    var autoAnalysisPaused = false;
    picker.required = false;

    function showMessage(text) {
      message.textContent = text;
      message.hidden = !text;
    }

    function updateQueue() {
      queueArea.hidden = entries.length === 0;
      entries.forEach(function (entry) { entry.retry.disabled = running || stale; });
      if (stale) document.querySelectorAll("[data-import-mapping] select, [data-import-mapping] button").forEach(function (input) { input.disabled = true; });
      if (stale) document.querySelectorAll("[data-bank-quick]").forEach(function (input) { input.disabled = true; });
      updateReviewControls();
    }

    function snapshotFields() {
      if (running) return activeFields;
      var fields = new FormData(form);
      fields.delete("statement");
      return fields;
    }

    function state(entry, value, error) {
      entry.state = value;
      entry.element.dataset.uploadState = value;
      var text = labels[value] || labels.error;
      if (value === "imported") {
        text = labels.compared.replace("%d", String(entry.result.added)).replace("%d", String(entry.result.existing));
        if (entry.result.preserved) text += " · " + labels.preserved.replace("%d", String(entry.result.preserved));
        entry.file = null;
      }
      if (value === "closed") entry.file = null;
      if (value === "uploading") text = labels.uploading.replace("%d", "0");
      entry.status.textContent = text;
      entry.error.textContent = error || "";
      entry.error.hidden = !error;
      entry.progress.hidden = value !== "uploading" && value !== "processing";
      if (value === "uploading") entry.progress.value = 0;
      if (value === "processing") entry.progress.removeAttribute("value");
      entry.remove.disabled = value === "uploading" || value === "processing";
      entry.retry.hidden = !["error", "paused"].includes(value) || !entry.valid || !entry.file;
      entry.retry.textContent = value === "paused" ? labels.resume : labels.retry;
      entry.link.hidden = value !== "imported" && value !== "mapping";
      if (!entry.link.hidden) {
        entry.link.href = value === "mapping" ? new URL(entry.result.url, location.origin).hash : entry.result.url;
        if (value === "mapping") entry.link.removeAttribute("target");
        else entry.link.target = "_blank";
        entry.link.textContent = value === "mapping" ? labels.mapLink : labels.review;
      }
      entry.actions.hidden = entry.retry.hidden && entry.link.hidden;
      updateQueue();
    }

    function addFiles(files) {
      showMessage("");
      Array.from(files).forEach(function (file) {
        var key = [file.name, file.size, file.lastModified].join("\u0000");
        var previous = entries.find(function (entry) { return entry.key === key; });
        if (previous && ["queued", "uploading", "processing", "mapping", "paused"].includes(previous.state)) {
          showMessage(labels.duplicate);
          return;
        }
        if (previous) {
          previous.file = null;
          previous.element.remove();
          entries = entries.filter(function (entry) { return entry !== previous; });
        }
        var element = template.content.firstElementChild.cloneNode(true);
        var entry = {
          key: key, file: file, fields: snapshotFields(), element: element, state: "queued", result: null,
          valid: /\.(pdf|csv|xlsx)$/i.test(file.name) && file.size > 0 && file.size <= Number(form.dataset.maxBytes),
          status: element.querySelector("[data-upload-state-label]"),
          error: element.querySelector("[data-upload-error]"),
          progress: element.querySelector("[data-upload-progress]"),
          remove: element.querySelector("[data-upload-remove]"),
          retry: element.querySelector("[data-upload-retry]"),
          link: element.querySelector("[data-upload-link]"),
          actions: element.querySelector("[data-upload-actions]")
        };
        element.querySelector("[data-upload-name]").textContent = file.name;
        entry.progress.setAttribute("aria-label", file.name);
        entry.remove.addEventListener("click", function () {
          if (entry.state === "uploading" || entry.state === "processing") return;
          entry.state = "removed";
          entry.file = null;
          entries = entries.filter(function (candidate) { return candidate !== entry; });
          element.remove();
          updateQueue();
        });
        entry.retry.addEventListener("click", function () {
          if (running || stale || !entry.file || !form.reportValidity()) return;
          entry.fields = snapshotFields();
          entry.result = null;
          entry.element.querySelector("[data-upload-detection]").hidden = true;
          paused = false;
          entries.forEach(function (pending) {
            if (pending.state === "paused") state(pending, "queued");
          });
          state(entry, "queued");
          runQueue();
        });
        entries.push(entry);
        queue.appendChild(element);
        state(entry, stale || !entry.valid ? "error" : paused ? "paused" : "queued",
          stale ? labels.context : entry.valid ? "" : labels.invalid);
      });
      updateQueue();
      runQueue();
    }

    picker.addEventListener("change", function () {
      addFiles(picker.files);
      picker.value = "";
    });
    dropzone.addEventListener("keydown", function (event) {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        picker.click();
      }
    });
    dropzone.addEventListener("dragenter", function (event) {
      event.preventDefault();
      dragDepth++;
      dropzone.dataset.dragging = "true";
    });
    dropzone.addEventListener("dragover", function (event) {
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
    });
    dropzone.addEventListener("dragleave", function () {
      dragDepth--;
      if (dragDepth <= 0) delete dropzone.dataset.dragging;
    });
    dropzone.addEventListener("drop", function (event) {
      event.preventDefault();
      dragDepth = 0;
      delete dropzone.dataset.dragging;
      if (event.dataTransfer.files.length) addFiles(event.dataTransfer.files);
      else showMessage(labels.empty);
    });
    ["dragover", "drop"].forEach(function (name) {
      document.addEventListener(name, function (event) {
        if (!event.dataTransfer || !Array.from(event.dataTransfer.types).includes("Files")) return;
        event.preventDefault();
        if (name === "drop" && !dropzone.contains(event.target)) showMessage(labels.outside);
      });
    });
    form.addEventListener("change", function (event) {
      if (event.target === picker || running || stale || paused) return;
      var fields = snapshotFields();
      entries.forEach(function (entry) {
        if (entry.state === "queued") entry.fields = fields;
      });
      runQueue();
    });
    form.addEventListener("invalid", function (event) {
      var panel = event.target.closest("details");
      if (panel) panel.open = true;
    }, true);

    function readOutcome(text) {
      var result = JSON.parse(text);
      if (!result || !["imported", "mapping"].includes(result.status) ||
          typeof result.url !== "string" || !/^[a-f0-9]{64}$/.test(result.digest) ||
          !Number.isInteger(result.transactions) || result.transactions < 1 || result.transactions > 1000 ||
          !Number.isInteger(result.accounts) || result.accounts < 1 || result.accounts > result.transactions) {
        throw new Error("Invalid upload response");
      }
      var target = new URL(result.url, location.origin);
      if (target.origin !== location.origin || target.username || target.password || target.pathname !== "/imports" ||
          (result.status === "mapping" ? (!/^#import-mapping-[A-Za-z0-9_-]{20,128}$/.test(target.hash) ||
            target.searchParams.get("preview") !== target.hash.slice("#import-mapping-".length)) : target.hash !== "")) {
        throw new Error("Invalid upload destination");
      }
      result.url = target.pathname + target.search + target.hash;
      if (result.status === "imported" && (!Number.isInteger(result.added) || !Number.isInteger(result.existing) ||
          !Number.isInteger(result.preserved) || result.preserved < 0 || result.preserved > result.existing ||
          result.added < 0 || result.existing < 0 || result.added + result.existing !== result.transactions)) {
        throw new Error("Invalid reconciliation counts");
      }
      if (result.detected !== undefined && (!Array.isArray(result.detected) ||
          result.detected.length > 20 || result.detected.some(function (line) {
            return typeof line !== "string" || line.length > 2048;
          }))) throw new Error("Invalid detection details");
      if (result.analysis_ids !== undefined && (!Array.isArray(result.analysis_ids) ||
          result.analysis_ids.length !== result.added || result.analysis_ids.some(function (id) { return typeof id !== "string" || !/^[1-9][0-9]{0,18}$/.test(id); }) ||
          new Set(result.analysis_ids).size !== result.analysis_ids.length)) throw new Error("Invalid analysis IDs");
      return result;
    }

    function send(entry, fields) {
      uploadRevision++;
      return new Promise(function (resolve) {
        var body = new FormData();
        fields.forEach(function (value, key) { if (key !== "statement") body.append(key, value); });
        body.append("statement", entry.file, entry.file.name);
        var xhr = new XMLHttpRequest();
        var settled = false;
        function fail(text, pause) {
          if (settled) return;
          settled = true;
          state(entry, "error", text);
          resolve(pause);
        }
        state(entry, "uploading");
        xhr.open("POST", "/imports/upload");
        xhr.setRequestHeader("Accept", "application/json");
        xhr.timeout = 210000;
        xhr.upload.addEventListener("progress", function (event) {
          if (settled || !event.lengthComputable) return;
          var percent = Math.min(100, Math.round(event.loaded * 100 / event.total));
          entry.progress.value = percent;
          entry.status.textContent = labels.uploading.replace("%d", String(percent));
        });
        xhr.upload.addEventListener("load", function () {
          if (!settled) state(entry, "processing");
        });
        xhr.addEventListener("load", function () {
          if (xhr.status < 200 || xhr.status >= 300) {
            var plain = (xhr.getResponseHeader("Content-Type") || "").startsWith("text/plain");
            var reason = plain ? xhr.responseText.trim().slice(0, 1000) : labels.http.replace("%d", String(xhr.status));
            if (reason === labels.context || reason === labels.household) stale = true;
            if (xhr.getResponseHeader("X-Import-Configure") === "1") {
              form.querySelector("[data-table-options]").open = true;
            }
            fail(reason || labels.response, xhr.status === 429 || stale);
            return;
          }
          try {
            entry.result = readOutcome(xhr.responseText);
          } catch {
            fail(labels.response, true);
            return;
          }
          settled = true;
          var detected = entry.element.querySelector("[data-upload-detected]");
          var details = entry.element.querySelector("[data-upload-detection]");
          detected.textContent = (entry.result.detected || []).join("\n");
          details.hidden = !detected.textContent;
          state(entry, entry.result.status);
          resolve(false);
        });
        ["error", "timeout", "abort"].forEach(function (name) {
          xhr.addEventListener(name, function () { fail(labels.network, true); });
        });
        try {
          xhr.send(body);
        } catch {
          fail(labels.network, true);
        }
      });
    }

    function reconcileMappings() {
      var history = document.getElementById("import-upload-history");
      if (!history) return;
      if (history.dataset.uploadGeneration !== form.elements.upload_generation.value) {
        stale = true;
        showMessage(labels.context);
        updateQueue();
      }
      var pending = Array.from(history.querySelectorAll("[data-pending-upload-url]")).map(function (node) {
        return node.dataset.pendingUploadUrl;
      });
      entries.forEach(function (entry) {
        if (entry.state !== "mapping" || pending.includes(entry.result.url)) return;
        state(entry, "closed");
      });
    }

    async function refreshPart(url, id, discardEdits) {
      var revision = id === "import-upload-history" ? ++historyRevision : ++reviewRevision;
      var response = await fetch(url, { credentials: "same-origin", cache: "no-store", redirect: "error" });
      if (!response.ok) throw new Error("Import status request failed");
      var parsed = new DOMParser().parseFromString(await response.text(), "text/html");
      var replacement = parsed.getElementById(id);
      var current = document.getElementById(id);
      if (!replacement || !current) throw new Error("Import status fragment missing");
      if (id === "import-upload-history") {
        if (revision !== historyRevision) return false;
        var mappings = current.querySelector("[data-mapping-list]");
        var incoming = Array.from(replacement.querySelectorAll("[data-import-mapping]"));
        var sameGeneration = current.dataset.uploadGeneration === replacement.dataset.uploadGeneration;
        Array.from(mappings.children).forEach(function (card) {
          if (!sameGeneration || !incoming.some(function (fresh) { return fresh.id === card.id; })) {
            if (card.hasAttribute("data-import-dirty") && !card.hasAttribute("data-mapping-busy")) showMessage(labels.mappingClosed);
            card.remove();
          }
        });
        incoming.forEach(function (fresh) {
            var previous = document.getElementById(fresh.id);
            if (!previous || !mappings.contains(previous)) { mappings.appendChild(fresh); return; }
            if (!previous.hasAttribute("data-mapping-busy")) {
              fresh.querySelectorAll("select").forEach(function (select) {
                var existing = Array.from(previous.querySelectorAll("select")).find(function (node) { return node.name === select.name; });
                if (!existing || existing.innerHTML === select.innerHTML) return;
                var value = existing.value;
                existing.replaceChildren.apply(existing, Array.from(select.childNodes));
                existing.value = value;
                if (value && existing.value !== value) {
                  existing.value = "";
                  mappingError(previous, labels.mappingAccountsChanged);
                }
              });
            }
        });
        current.hidden = replacement.hidden;
        current.dataset.uploadGeneration = replacement.dataset.uploadGeneration;
        return true;
      }
      if (id === "import-review") {
        if (revision !== reviewRevision) return false;
        if (current.dataset.reviewMonth !== replacement.dataset.reviewMonth) {
          if (!discardEdits && reviewEditing()) { reviewDeferred.hidden = false; return false; }
        } else {
          var list = current.querySelector("[data-bank-rows]");
          var rows = Array.from(replacement.querySelectorAll("[data-bank-row]"));
          Array.from(list.querySelectorAll("[data-bank-row]")).forEach(function (row) {
            if (!rows.some(function (fresh) { return fresh.id === row.id; })) row.remove();
          });
          rows.forEach(function (fresh, index) {
            var old = document.getElementById(fresh.id);
            if (old && list.contains(old)) {
              var dirty = old.querySelector("[data-import-dirty]");
              if (!dirty && !old.hasAttribute("data-review-busy") &&
                  (reviewReplaceIDs.has(old.dataset.bankRow) || (!old.querySelector(".import-payment[open]") && !reviewSelection.has(old.dataset.bankRow)))) {
                fresh.querySelector(".import-payment").open = old.querySelector(".import-payment").open;
                old.replaceWith(fresh);
              }
            } else {
              var next = rows.slice(index + 1).map(function (row) { return document.getElementById(row.id); }).find(function (node) { return node && list.contains(node); });
              list.insertBefore(fresh, next || null);
            }
          });
          var empty = list.querySelector("[data-review-empty]");
          if (empty) empty.remove();
          if (!rows.length) list.appendChild(replacement.querySelector("[data-review-empty]"));
          ["data-review-toolbar", "data-review-counts", "data-review-months"].forEach(function (selector) {
            current.querySelector("[" + selector + "]").replaceWith(replacement.querySelector("[" + selector + "]"));
          });
          ["data-review-filters", "data-bank-analyze"].forEach(function (selector) {
            current.querySelector("[" + selector + "]").replaceWith(replacement.querySelector("[" + selector + "]"));
          });
          replacement.querySelectorAll("[data-bank-bulk] select").forEach(function (fresh) {
            var existing = current.querySelector('[data-bank-bulk] select[name="' + fresh.name + '"]');
            var value = existing.value;
            existing.replaceChildren.apply(existing, Array.from(fresh.childNodes));
            existing.value = value;
          });
          updateReviewControls();
          return true;
        }
      }
      current.replaceWith(replacement);
      if (id === "import-review") updateReviewControls();
      return true;
    }

    function reviewEditing() {
      return reviewBusy || reviewSelection.size > 0 || document.querySelector("#import-review [data-import-dirty], #import-review [data-review-busy]");
    }

    async function showImported(url, interactive) {
      var destination = new URL(url, location.origin);
      var month = destination.searchParams.get("m");
      if (destination.origin !== location.origin || destination.pathname !== "/imports" || !/^\d{4}-(0[1-9]|1[0-2])$/.test(month || "")) {
        throw new Error("Invalid review month");
      }
      var current = document.getElementById("import-review");
      var discard = false;
      if (current.dataset.reviewMonth !== month && reviewEditing()) {
        if (!interactive || !window.confirm(labels.discardEdits)) {
          reviewDeferred.hidden = false;
          reviewDeferred.querySelector("a").href = destination.pathname + destination.search;
          return;
        }
        discard = true;
        reviewSelection.clear();
      }
      var target = new URL(form.dataset.reviewUrl, location.origin);
      target.searchParams.set("m", month);
      if (!await refreshPart(target.pathname + target.search, "import-review", discard)) return;
      form.dataset.reviewUrl = target.pathname + target.search;
      var historyTarget = new URL(form.dataset.historyUrl, location.origin);
      historyTarget.searchParams.set("m", month);
      form.dataset.historyUrl = historyTarget.pathname + historyTarget.search;
      reviewDeferred.hidden = true;
      window.history.replaceState(null, "", destination.pathname + destination.search + "#import-review");
      current = document.getElementById("import-review");
      current.scrollIntoView({ block: "start" });
      if (!reviewEditing()) current.focus({ preventScroll: true });
    }

    async function refreshHistory() {
      if (await refreshPart(form.dataset.historyUrl, "import-upload-history")) reconcileMappings();
    }

    async function refreshStatus() {
      if (running) return;
      if (refreshing) { refreshAgain = true; return; }
      refreshing = true;
      refreshAgain = false;
      var revision = uploadRevision;
      refreshError.hidden = true;
      try {
        await refreshHistory();
        if (running || revision !== uploadRevision) { refreshAgain = true; return; }
        await refreshPart(form.dataset.reviewUrl, "import-review");
      } catch {
        refreshError.hidden = false;
      } finally {
        refreshing = false;
        if (refreshAgain && !running) refreshStatus();
      }
    }

    async function runQueue() {
      if (running || stale || paused || !entries.some(function (entry) { return entry.state === "queued"; })) return;
      if (form.elements.account.disabled || !form.elements.account.value) { showMessage(labels.account); return; }
      if (!form.reportValidity()) return;
      showMessage("");
      activeFields = snapshotFields();
      var controls = Array.from(form.querySelectorAll('select, input:not([type="file"]):not([type="hidden"])')).map(function (input) {
        var saved = { input: input, disabled: input.disabled };
        input.disabled = true;
        return saved;
      });
      running = true;
      followedUpload = false;
      runningHint.hidden = false;
      updateQueue();
      try {
        var entry;
        while ((entry = entries.find(function (candidate) { return candidate.state === "queued"; }))) {
          var stop = await send(entry, entry.fields);
          try {
            await refreshHistory();
            if (entry.state === "imported" && !followedUpload) {
              followedUpload = true;
              await showImported(entry.result.url, false);
            }
            if (entry.state === "imported") await autoAnalyze(entry.result);
          } catch {
            refreshError.hidden = false;
          }
          if (stop || stale) {
            paused = true;
            entries.forEach(function (pending) {
              if (pending.state === "queued") state(pending, stale ? "error" : "paused", stale ? labels.context : "");
            });
            break;
          }
        }
      } finally {
        running = false;
        runningHint.hidden = true;
        controls.forEach(function (saved) { saved.input.disabled = saved.disabled; });
        activeFields = null;
        updateQueue();
        await refreshStatus();
      }
    }
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      runQueue();
    });

    function mappingError(card, text) {
      var error = card.querySelector("[data-mapping-error]");
      error.textContent = text;
      error.hidden = !text;
      if (text && card.hasAttribute("data-mapping-busy")) error.scrollIntoView({ block: "nearest" });
    }

    document.addEventListener("submit", async function (event) {
      var mappingForm = event.target;
      var card = mappingForm.closest("[data-import-mapping]");
      if (!card) return;
      event.preventDefault();
      if (card.hasAttribute("data-mapping-busy")) return;
      if (stale) { mappingError(card, labels.context); return; }
      if (!mappingForm.reportValidity()) return;
      var target = new URL(mappingForm.action, location.origin);
      if (target.origin !== location.origin || !["/imports/mapping", "/imports/mapping/cancel"].includes(target.pathname)) {
        mappingError(card, labels.response);
        return;
      }
      var body = new URLSearchParams(new FormData(mappingForm));
      var pendingURL = card.dataset.pendingUploadUrl;
      card.setAttribute("data-mapping-busy", "");
      card.setAttribute("aria-busy", "true");
      mappingError(card, "");
      var controls = Array.from(card.querySelectorAll("button, select"));
      controls.forEach(function (input) { input.disabled = true; });
      try {
        var response = await fetch(target.pathname, {
          method: "POST", body: body, credentials: "same-origin", cache: "no-store",
          redirect: "error", headers: { Accept: "application/json" }
        });
        if (!response.ok) {
          var reason = (response.headers.get("Content-Type") || "").startsWith("text/plain") ?
            (await response.text()).trim().slice(0, 1000) : labels.http.replace("%d", String(response.status));
          if (reason === labels.context || reason === labels.household) stale = true;
          mappingError(card, reason || labels.response);
          return;
        }
        var result = null;
        if (target.pathname === "/imports/mapping") {
          result = readOutcome(await response.text());
          if (result.status !== "imported") throw new Error("Invalid mapping confirmation");
        } else if (response.status !== 204) {
          throw new Error("Invalid mapping cancellation");
        }
        entries.forEach(function (entry) {
          if (entry.state !== "mapping" || entry.result.url !== pendingURL) return;
          if (result) { entry.result = result; state(entry, "imported"); }
          else state(entry, "closed");
        });
        card.remove();
        if (result) await showImported(result.url, false);
        if (result) await autoAnalyze(result);
      } catch {
        mappingError(card, labels.network);
      } finally {
        card.removeAttribute("data-mapping-busy");
        card.removeAttribute("aria-busy");
        controls.forEach(function (input) { input.disabled = stale; });
        try { await refreshHistory(); } catch { refreshError.hidden = false; }
        await refreshStatus();
      }
    });

    document.addEventListener("click", async function (event) {
      var link = event.target.closest("a");
      if (!link) return;
      if (link.hasAttribute("data-review-month-link") || link.closest("[data-upload-review-deferred]") ||
          (link.hasAttribute("data-upload-link") && !new URL(link.href, location.origin).hash)) {
        event.preventDefault();
        try { await showImported(link.href, true); } catch { refreshError.hidden = false; }
        return;
      }
      if (!link.hasAttribute("data-upload-link")) return;
      var hash = new URL(link.href, location.origin).hash;
      if (!hash.startsWith("#import-mapping-")) return;
      event.preventDefault();
      try {
        if (!document.getElementById(hash.slice(1))) await refreshHistory();
        var card = document.getElementById(hash.slice(1));
        if (!card) { showMessage(labels.mappingClosed); return; }
        card.scrollIntoView({ block: "nearest" });
        card.focus({ preventScroll: true });
      } catch {
        refreshError.hidden = false;
      }
    });
    document.addEventListener("input", function (event) {
      var reviewForm = event.target.closest("#import-review .import-payment form");
      if (reviewForm) markReviewDirty(reviewForm);
    });
    document.addEventListener("change", function (event) {
      var reviewForm = event.target.closest("#import-review .import-payment form");
      if (reviewForm) markReviewDirty(reviewForm);
      var mapping = event.target.closest("[data-import-mapping]");
      if (mapping) mapping.dataset.importDirty = "true";
    });
    window.addEventListener("focus", refreshStatus);
    window.addEventListener("pageshow", refreshStatus);
    window.addEventListener("beforeunload", function (event) {
      if (running || reviewEditing() || document.querySelector("[data-mapping-busy], [data-import-mapping][data-import-dirty]") ||
          entries.some(function (entry) { return entry.state === "queued" || entry.state === "paused"; })) {
        event.preventDefault();
        event.returnValue = "";
      }
    });

    function markReviewDirty(reviewForm) {
      reviewForm.dataset.importDirty = "true";
      var button = reviewForm.closest("[data-bank-row]").querySelector("[data-bank-quick]");
      if (button) { button.disabled = true; button.title = labels.reviewChanged; }
      reviewSelection.delete(reviewForm.closest("[data-bank-row]").dataset.bankRow);
      updateReviewControls();
    }

    function reviewNotice(text, isError) {
      var target = document.querySelector(isError ? "[data-review-operation-error]" : "[data-review-operation-status]");
      if (isError && text && !target.hidden && target.textContent) {
        if (!target.textContent.includes(text)) target.textContent += " " + text;
        return;
      }
      target.textContent = text;
      target.hidden = !text;
    }

    function updateReviewControls() {
      var rows = Array.from(document.querySelectorAll("#import-review [data-bank-row]"));
      var present = new Set();
      rows.forEach(function (row) {
        var id = row.dataset.bankRow;
        present.add(id);
        var checkbox = row.querySelector("[data-review-select]");
        var busy = reviewBusy || stale || row.hasAttribute("data-review-busy");
        var dirty = !!row.querySelector("[data-import-dirty]");
        checkbox.disabled = busy || dirty || !row.querySelector("[data-bank-review-form]");
        if (reviewSelection.has(id) && (reviewSelection.get(id) !== row.dataset.reviewToken || !row.querySelector("[data-bank-review-form]") || dirty)) {
          reviewSelection.delete(id);
          reviewNotice(labels.selectionChanged, true);
        }
        checkbox.checked = reviewSelection.has(id);
        row.hidden = reviewFilter !== "all" && row.dataset.reviewStage !== reviewFilter;
        var quick = row.querySelector("[data-bank-quick]");
        if (quick) quick.disabled = busy || dirty || row.dataset.reviewQuick !== "true";
        var pair = row.querySelector("[data-bank-pair]");
        if (pair) pair.querySelector("button").disabled = busy || dirty || !pair.elements.pair_token.value;
        var copy = row.querySelector("[data-copy-classification]");
        if (copy) copy.disabled = busy || Number(copy.dataset.copyCount) === 0;
      });
      reviewSelection.forEach(function (_, id) { if (!present.has(id)) reviewSelection.delete(id); });
      document.querySelectorAll("[data-review-filter]").forEach(function (button) {
        button.setAttribute("aria-pressed", String(button.dataset.reviewFilter === reviewFilter));
        button.disabled = reviewBusy;
      });
      document.querySelector("[data-selected-count]").textContent = String(reviewSelection.size);
      var selected = rows.filter(function (row) { return reviewSelection.has(row.dataset.bankRow); });
      document.querySelector("[data-bulk-accept]").disabled = stale || reviewBusy || !selected.length ||
        selected.some(function (row) { return row.dataset.reviewQuick !== "true"; });
      var bulk = document.querySelector("[data-bank-bulk]");
      Array.from(bulk.elements.category.options).forEach(function (option) {
        option.disabled = !!option.value && selected.some(function (row) { return row.dataset.reviewDirection !== option.dataset.direction; });
      });
      var category = bulk.elements.category.selectedOptions[0];
      bulk.querySelector("[data-bulk-category]").disabled = stale || reviewBusy || !selected.length || !category || !category.value || category.disabled;
      document.querySelector("[data-select-ready]").disabled = stale || reviewBusy ||
        !rows.some(function (row) { return !row.hidden && row.dataset.reviewStage === "ready" && !row.querySelector("[data-review-select]").disabled; });
      document.querySelector("[data-select-visible]").disabled = stale || reviewBusy ||
        !rows.some(function (row) { return !row.hidden && !row.querySelector("[data-review-select]").disabled; });
      document.querySelector("[data-clear-selection]").disabled = reviewBusy || !reviewSelection.size;
      var analyze = document.querySelector("[data-bank-analyze] button");
      analyze.disabled = stale || reviewBusy || analyze.dataset.aiAvailable !== "true" || Number(analyze.dataset.aiCount) === 0;
      form.dataset.autoAnalyze = analyze.dataset.aiAvailable;
    }

    async function reviewRequest(path, body, analysis) {
      if (stale || reviewBusy) { reviewNotice(stale ? labels.context : labels.aiRunning, true); return false; }
      var ids = body.getAll("payment");
      if (ids.length > 1000) { reviewNotice(labels.limit, true); return false; }
      var targets = ids.length ? ids.map(function (id) { return document.querySelector('[data-bank-row="' + id + '"]'); }).filter(Boolean) :
        Array.from(document.querySelectorAll('#import-review [data-bank-row]:not([data-review-stage="done"])'));
      if (body.get("operation") === "transfer") {
        document.querySelectorAll("[data-bank-pair]").forEach(function (pair) {
          var row = pair.closest("[data-bank-row]");
          if (pair.elements.pair_token.value === body.get("pair_token") && !targets.includes(row)) targets.push(row);
        });
      }
      if (targets.some(function (row) { return row.querySelector("[data-import-dirty]") || row.hasAttribute("data-review-busy"); })) {
        reviewNotice(labels.dirty, true);
        return false;
      }
      reviewBusy = true;
      reviewNotice("", true);
      reviewNotice(analysis ? labels.aiRunning : "", false);
      var disabled = [];
      targets.forEach(function (row) {
        row.setAttribute("data-review-busy", "");
        row.querySelectorAll("button, input, select, textarea").forEach(function (input) {
          disabled.push({input:input, disabled:input.disabled});
          input.disabled = true;
        });
      });
      updateReviewControls();
      var successful = false;
      var statusText = "";
      try {
        var response = await fetch(path, {
          method:"POST", body:body, credentials:"same-origin", redirect:"error", cache:"no-store",
          headers:{Accept:"application/json"}
        });
        if (!response.ok) {
          var reason = (response.headers.get("Content-Type") || "").startsWith("text/plain") ?
            (await response.text()).trim().slice(0,1000) : labels.http.replace("%d",String(response.status));
          if (reason === labels.context || reason === labels.household) stale = true;
          reviewNotice(reason || labels.response, true);
        } else {
          var result = await response.json();
          if (!Number.isInteger(result.updated) || result.updated < 0 || result.updated > 1000) throw new Error("Invalid review action response");
          successful = true;
          statusText = (analysis ? labels.aiComplete : labels.updated).replace("%d", String(result.updated));
          if (!analysis) reviewSelection.clear();
        }
      } catch {
        reviewNotice(analysis ? labels.aiFailed : labels.network, true);
      } finally {
        reviewBusy = false;
        targets.forEach(function (row) { row.removeAttribute("data-review-busy"); reviewReplaceIDs.add(row.dataset.bankRow); });
        if (body.get("operation") === "transfer") {
          document.querySelectorAll("[data-bank-pair]").forEach(function (pair) {
            if (pair.elements.pair_token.value === body.get("pair_token")) reviewReplaceIDs.add(pair.closest("[data-bank-row]").dataset.bankRow);
          });
        }
        disabled.forEach(function (saved) { saved.input.disabled = stale || saved.disabled; });
        try {
          await refreshHistory();
          await refreshPart(form.dataset.reviewUrl, "import-review");
        } catch { refreshError.hidden = false; }
        reviewReplaceIDs.clear();
        reviewNotice(statusText, false);
        updateReviewControls();
      }
      return successful;
    }

    async function autoAnalyze(result) {
      if (form.dataset.autoAnalyze !== "true" || autoAnalysisPaused || !result.analysis_ids || !result.analysis_ids.length) return;
      var body = new URLSearchParams();
      body.set("household", form.elements.household.value);
      body.set("upload_generation", form.elements.upload_generation.value);
      body.set("month", document.getElementById("import-review").dataset.reviewMonth);
      result.analysis_ids.forEach(function (id) { body.append("payment", String(id)); });
      if (!await reviewRequest("/imports/analyze", body, true)) autoAnalysisPaused = true;
    }

    document.addEventListener("change", function (event) {
      if (event.target.hasAttribute("data-review-select")) {
        var row = event.target.closest("[data-bank-row]");
        if (event.target.checked) reviewSelection.set(row.dataset.bankRow, row.dataset.reviewToken);
        else reviewSelection.delete(row.dataset.bankRow);
        updateReviewControls();
      } else if (event.target.closest("[data-bank-bulk]")) updateReviewControls();
    });
    document.addEventListener("click", function (event) {
      var button = event.target.closest("button");
      if (!button || button.disabled) return;
      if (button.hasAttribute("data-review-filter")) {
        reviewFilter = button.dataset.reviewFilter;
        reviewSelection.clear();
      } else if (button.hasAttribute("data-clear-selection")) {
        reviewSelection.clear();
      } else if (button.hasAttribute("data-select-ready") || button.hasAttribute("data-select-visible")) {
        reviewSelection.clear();
        document.querySelectorAll('[data-bank-row]').forEach(function (row) {
          if (!row.hidden && !row.querySelector("[data-review-select]").disabled &&
              (button.hasAttribute("data-select-visible") || row.dataset.reviewStage === "ready")) reviewSelection.set(row.dataset.bankRow,row.dataset.reviewToken);
        });
      } else if (button.hasAttribute("data-copy-classification")) {
        var source = button.closest("[data-bank-row]");
        var bulk = document.querySelector("[data-bank-bulk]");
        reviewFilter = "all";
        reviewSelection.clear();
        document.querySelectorAll("[data-bank-row]").forEach(function (row) {
          if (row.dataset.reviewMatch === source.dataset.reviewMatch && !row.querySelector("[data-review-select]").disabled) reviewSelection.set(row.dataset.bankRow,row.dataset.reviewToken);
        });
        bulk.elements.category.value = button.dataset.copyCategory;
        bulk.elements.nature.value = button.dataset.copyNature;
        bulk.elements.class.value = button.dataset.copyClass;
        bulk.scrollIntoView({block:"center"});
      } else return;
      if (reviewSelection.size > 1000) { reviewSelection.clear(); reviewNotice(labels.limit,true); }
      updateReviewControls();
    });
    document.addEventListener("submit", async function (event) {
      var actionForm = event.target;
      var analysis = actionForm.hasAttribute("data-bank-analyze");
      var pair = actionForm.hasAttribute("data-bank-pair");
      var bulk = actionForm.hasAttribute("data-bank-bulk");
      if (!analysis && !pair && !bulk) return;
      event.preventDefault();
      if (event.submitter && event.submitter.disabled) return;
      var body = new URLSearchParams(new FormData(actionForm));
      if (bulk) {
        if (!reviewSelection.size) { reviewNotice(labels.noSelection,true); return; }
        body.set("operation", event.submitter ? event.submitter.value : "category");
        reviewSelection.forEach(function (token,id) { body.append("payment",id); body.set("token_"+id,token); });
      }
      if (pair && !window.confirm(labels.pairConfirm)) return;
      if (analysis) autoAnalysisPaused = false;
      await reviewRequest(analysis ? "/imports/analyze" : "/imports/review-batch",body,analysis);
    });

    document.addEventListener("submit", async function (event) {
      var reviewForm = event.target;
      if (reviewForm.hasAttribute("data-import-month-form")) {
        event.preventDefault();
        var target = new URL("/imports", location.origin);
        target.searchParams.set("m", new FormData(reviewForm).get("m"));
        try { await showImported(target.href, true); } catch { refreshError.hidden = false; }
        return;
      }
      if (!reviewForm.hasAttribute("data-bank-review-form")) {
        if (reviewForm.closest("[data-bank-row]")) reviewForm.removeAttribute("data-import-dirty");
        return;
      }
      if (!event.submitter || !event.submitter.hasAttribute("data-bank-quick")) {
        reviewForm.removeAttribute("data-import-dirty");
        return;
      }
      event.preventDefault();
      var row = reviewForm.closest("[data-bank-row]");
      if (row.hasAttribute("data-review-busy")) return;
      var wasOpen = row.querySelector(".import-payment").open;
      var errorText = "";
      if (stale || reviewForm.hasAttribute("data-import-dirty")) {
        errorText = stale ? labels.context : labels.reviewChanged;
      } else {
        var body = new URLSearchParams(new FormData(reviewForm));
        body.set("quick", "yes");
        var target = new URL(reviewForm.action, location.origin);
        if (target.origin !== location.origin || target.pathname !== "/imports/transactions/" + row.dataset.bankRow) {
          errorText = labels.response;
        } else {
          row.setAttribute("data-review-busy", "");
          row.setAttribute("aria-busy", "true");
          var controls = Array.from(row.querySelectorAll("button, input, select, textarea")).map(function (input) {
            var saved = { input: input, disabled: input.disabled };
            input.disabled = true;
            return saved;
          });
          try {
            var response = await fetch(target.pathname, {
              method: "POST", body: body, credentials: "same-origin", redirect: "error",
              headers: { Accept: "application/json" }
            });
            if (!response.ok) {
              errorText = (response.headers.get("Content-Type") || "").startsWith("text/plain") ?
                (await response.text()).trim().slice(0, 1000) : labels.http.replace("%d", String(response.status));
              if (errorText === labels.context || errorText === labels.household) stale = true;
            } else {
              var result = await response.json();
              if (result.id !== row.dataset.bankRow || typeof result.html !== "string") throw new Error("Invalid review response");
              var parsed = new DOMParser().parseFromString(result.html, "text/html");
              var replacement = parsed.getElementById(row.id);
              if (!replacement || replacement.dataset.bankRow !== row.dataset.bankRow) throw new Error("Missing reviewed row");
              row.replaceWith(replacement);
            }
          } catch {
            errorText = labels.network;
          } finally {
            row.removeAttribute("data-review-busy");
            row.removeAttribute("aria-busy");
            controls.forEach(function (saved) { saved.input.disabled = stale || saved.disabled; });
          }
          if (errorText) row.querySelector(".import-payment").open = false;
          try {
            await refreshHistory();
            await refreshPart(form.dataset.reviewUrl, "import-review");
          } catch { refreshError.hidden = false; }
        }
      }
      if (errorText) {
        var current = document.getElementById(row.id);
        if (current) {
          current.querySelector(".import-payment").open = wasOpen;
          var error = current.querySelector("[data-payment-error]");
          error.textContent = errorText;
          error.hidden = false;
          error.scrollIntoView({ block: "nearest" });
        } else showMessage(errorText);
      }
    });
    updateQueue();
  });
})();
