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
    var start = form.querySelector('button[type="submit"]');
    var message = form.querySelector("[data-upload-message]");
    var runningHint = form.querySelector("[data-bank-progress]");
    var refreshError = form.querySelector("[data-upload-refresh-error]");
    var reviewDeferred = form.querySelector("[data-upload-review-deferred]");
    var labels = form.querySelector("[data-upload-labels]").dataset;
    var entries = [];
    var running = false;
    var refreshing = false;
    var refreshAgain = false;
    var uploadRevision = 0;
    var stale = false;
    var dragDepth = 0;
    picker.required = false;

    function showMessage(text) {
      message.textContent = text;
      message.hidden = !text;
    }

    function updateStart() {
      var account = form.elements.account;
      start.disabled = running || stale || account.disabled || !account.value ||
        !entries.some(function (entry) { return entry.state === "queued"; });
      queueArea.hidden = entries.length === 0;
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
      entry.retry.hidden = value !== "error" || !entry.valid;
      entry.link.hidden = value !== "imported" && value !== "mapping";
      if (!entry.link.hidden) {
        entry.link.href = entry.result.url;
        entry.link.textContent = value === "mapping" ? labels.mapLink : labels.review;
      }
      entry.actions.hidden = entry.retry.hidden && entry.link.hidden;
      updateStart();
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
          key: key, file: file, element: element, state: "queued", result: null,
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
          updateStart();
        });
        entry.retry.addEventListener("click", function () {
          entry.result = null;
          state(entry, "queued");
        });
        entries.push(entry);
        queue.appendChild(element);
        state(entry, entry.valid ? "queued" : "error", entry.valid ? "" : labels.invalid);
      });
      updateStart();
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
    form.elements.account.addEventListener("change", updateStart);
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
      if (target.origin !== location.origin || target.username || target.password || target.hash ||
          !["/imports", "/imports/mapping"].includes(target.pathname)) {
        throw new Error("Invalid upload destination");
      }
      result.url = target.pathname + target.search;
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
        updateStart();
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
      var response = await fetch(url, { credentials: "same-origin", cache: "no-store", redirect: "error" });
      if (!response.ok) throw new Error("Import status request failed");
      var parsed = new DOMParser().parseFromString(await response.text(), "text/html");
      var replacement = parsed.getElementById(id);
      var current = document.getElementById(id);
      if (!replacement || !current) throw new Error("Import status fragment missing");
      if (id === "import-review" && current.querySelector(".import-payment[open], [data-import-dirty]")) {
        reviewDeferred.hidden = false;
        return;
      }
      current.replaceWith(replacement);
    }

    async function refreshStatus() {
      if (running) return;
      if (refreshing) { refreshAgain = true; return; }
      refreshing = true;
      refreshAgain = false;
      var revision = uploadRevision;
      refreshError.hidden = true;
      try {
        await refreshPart(form.dataset.historyUrl, "import-upload-history");
        if (running || revision !== uploadRevision) { refreshAgain = true; return; }
        reconcileMappings();
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

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      if (running || stale) return;
      var batch = entries.filter(function (entry) { return entry.state === "queued"; });
      if (!batch.length) { showMessage(labels.empty); return; }
      if (!form.reportValidity()) return;
      showMessage("");
      var fields = new FormData(form);
      var controls = Array.from(form.querySelectorAll('select, input:not([type="file"]):not([type="hidden"])')).map(function (input) {
        var saved = { input: input, disabled: input.disabled };
        input.disabled = true;
        return saved;
      });
      running = true;
      runningHint.hidden = false;
      updateStart();
      try {
        for (var entry of batch) {
          if (entry.state !== "queued") continue;
          if (await send(entry, fields)) break;
        }
      } finally {
        running = false;
        runningHint.hidden = true;
        controls.forEach(function (saved) { saved.input.disabled = saved.disabled; });
        updateStart();
        await refreshStatus();
      }
    });
    document.addEventListener("input", function (event) {
      var reviewForm = event.target.closest("#import-review .import-payment form");
      if (reviewForm) reviewForm.dataset.importDirty = "true";
    });
    document.addEventListener("change", function (event) {
      var reviewForm = event.target.closest("#import-review .import-payment form");
      if (reviewForm) reviewForm.dataset.importDirty = "true";
    });
    window.addEventListener("focus", refreshStatus);
    window.addEventListener("pageshow", refreshStatus);
    window.addEventListener("beforeunload", function (event) {
      if (running || entries.some(function (entry) { return entry.state === "queued"; })) {
        event.preventDefault();
        event.returnValue = "";
      }
    });
    updateStart();
  });
})();
