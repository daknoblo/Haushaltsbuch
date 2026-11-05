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
    var stale = false;
    var dragDepth = 0;
    picker.required = false;

    function showMessage(text) {
      message.textContent = text;
      message.hidden = !text;
    }

    function updateQueue() {
      queueArea.hidden = entries.length === 0;
      entries.forEach(function (entry) { entry.retry.disabled = running || stale; });
      if (stale) document.querySelectorAll("[data-import-mapping] select, [data-import-mapping] button").forEach(function (input) { input.disabled = true; });
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
        text = entry.result.transactions === 1 ? labels.importedOne :
          labels.imported.replace("%d", String(entry.result.transactions));
      }
      if (value === "uploading") text = labels.uploading.replace("%d", "0");
      entry.status.textContent = text;
      entry.error.textContent = error || "";
      entry.error.hidden = !error;
      entry.progress.hidden = value !== "uploading" && value !== "processing";
      if (value === "uploading") entry.progress.value = 0;
      if (value === "processing") entry.progress.removeAttribute("value");
      entry.remove.disabled = value === "uploading" || value === "processing";
      entry.retry.hidden = !["error", "paused"].includes(value) || !entry.valid;
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
        if (entries.some(function (entry) { return entry.key === key; })) {
          showMessage(labels.duplicate);
          return;
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
          entries = entries.filter(function (candidate) { return candidate !== entry; });
          element.remove();
          updateQueue();
        });
        entry.retry.addEventListener("click", function () {
          if (running || stale || !form.reportValidity()) return;
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
      if (result.detected !== undefined && (!Array.isArray(result.detected) ||
          result.detected.length > 20 || result.detected.some(function (line) {
            return typeof line !== "string" || line.length > 2048;
          }))) throw new Error("Invalid detection details");
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
        var file = Array.from(history.querySelectorAll("[data-uploaded-digest]")).find(function (node) {
          return node.dataset.uploadedDigest === entry.result.digest &&
            Number(node.dataset.uploadedTransactions) === entry.result.transactions &&
            Number(node.dataset.uploadedAccounts) === entry.result.accounts;
        });
        if (file && file.dataset.uploadedUrl) {
          entry.result.url = file.dataset.uploadedUrl;
          state(entry, "imported");
        } else {
          state(entry, "error", labels.mappingClosed);
        }
      });
    }

    async function refreshPart(url, id) {
      var revision = id === "import-upload-history" ? ++historyRevision : 0;
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
        current.querySelector("[data-uploaded-files]").replaceWith(replacement.querySelector("[data-uploaded-files]"));
        current.querySelector("[data-history-empty]").hidden = replacement.querySelector("[data-history-empty]").hidden;
        current.dataset.uploadGeneration = replacement.dataset.uploadGeneration;
        return true;
      }
      if (id === "import-review" && current.querySelector(".import-payment[open], [data-import-dirty]")) {
        reviewDeferred.hidden = false;
        return false;
      }
      current.replaceWith(replacement);
      return true;
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
        var editing = document.querySelector("#import-review .import-payment[open], #import-review [data-import-dirty]");
        reviewDeferred.hidden = !editing;
        if (!editing) await refreshPart(form.dataset.reviewUrl, "import-review");
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
      runningHint.hidden = false;
      updateQueue();
      try {
        var entry;
        while ((entry = entries.find(function (candidate) { return candidate.state === "queued"; }))) {
          var stop = await send(entry, entry.fields);
          try {
            await refreshHistory();
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
          else state(entry, "error", labels.mappingClosed);
        });
        card.remove();
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
      var refresh = link.hasAttribute("data-refresh-mapping");
      if (!refresh && !link.hasAttribute("data-upload-link")) return;
      var hash = new URL(link.href, location.origin).hash;
      if (!refresh && !(link.hasAttribute("data-upload-link") && hash.startsWith("#import-mapping-"))) return;
      event.preventDefault();
      try {
        if (refresh || !document.getElementById(hash.slice(1))) await refreshHistory();
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
      if (reviewForm) reviewForm.dataset.importDirty = "true";
    });
    document.addEventListener("change", function (event) {
      var reviewForm = event.target.closest("#import-review .import-payment form");
      if (reviewForm) reviewForm.dataset.importDirty = "true";
      var mapping = event.target.closest("[data-import-mapping]");
      if (mapping) mapping.dataset.importDirty = "true";
    });
    window.addEventListener("focus", refreshStatus);
    window.addEventListener("pageshow", refreshStatus);
    window.addEventListener("beforeunload", function (event) {
      if (running || document.querySelector("[data-mapping-busy], [data-import-mapping][data-import-dirty]") ||
          entries.some(function (entry) { return entry.state === "queued" || entry.state === "paused"; })) {
        event.preventDefault();
        event.returnValue = "";
      }
    });
    updateQueue();
  });
})();
