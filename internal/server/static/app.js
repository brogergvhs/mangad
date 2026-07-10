// Collapsible table rows: a .row-toggle reveals the detail row it points at.
// Clicks on interactive elements inside the row are ignored.
document.addEventListener("click", function (e) {
  if (e.target.closest("button, a, input, select, textarea, form, label")) return;
  var toggle = e.target.closest(".row-toggle");
  if (!toggle) return;
  var target = document.getElementById(toggle.dataset.target);
  if (!target) return;
  target.hidden = !target.hidden;
  toggle.classList.toggle("open");
});

(function () {
  var shell = document.querySelector("[data-reader]");
  if (!shell) return;
  var manifestTag = document.getElementById("reader-manifest");
  var strip = document.getElementById("reader-strip");
  var loadingEl = document.getElementById("reader-loading");
  var position = document.getElementById("reader-position");
  var prev = document.querySelector("[data-reader-prev]");
  var next = document.querySelector("[data-reader-next]");

  // --- controls & keyboard work even on the empty page ---
  document.addEventListener("click", function (e) {
    if (e.target.closest(".reader-controls")) return;
    if (window.matchMedia("(max-width: 1024px)").matches) {
      document.body.classList.toggle("controls-on");
    }
  });
  document.addEventListener("keydown", function (e) {
    if (e.target.closest("input, textarea, select")) return;
    if ((e.key === "ArrowLeft" || e.key === "[") && prev) {
      location.href = prev.href;
    } else if ((e.key === "ArrowRight" || e.key === "]") && next) {
      location.href = next.href;
    } else if (e.key === "j") {
      window.scrollBy({ top: window.innerHeight * 0.9, behavior: "smooth" });
    } else if (e.key === "k") {
      window.scrollBy({ top: -window.innerHeight * 0.9, behavior: "smooth" });
    }
  });

  function buildFailed(msg) {
    if (loadingEl) {
      loadingEl.textContent = msg;
      loadingEl.classList.add("reader-page-failed");
    }
  }
  if (!manifestTag || !strip) {
    buildFailed("Reader failed to initialise.");
    return;
  }
  var manifest;
  try {
    manifest = JSON.parse(manifestTag.textContent);
  } catch (err) {
    buildFailed("Reader failed to parse the chapter manifest.");
    return;
  }

  // Flatten the manifest into an ordered build queue. Elements are appended
  // strictly in order, and each image is appended only after it has fully
  // loaded (dimensions known), so the layout never collapses and a later
  // chapter can never appear before every earlier page is in place.
  var queue = [];
  (manifest.chapters || []).forEach(function (chapter) {
    queue.push({ kind: "sep", label: chapter.label });
    (chapter.pages || []).forEach(function (page) {
      queue.push({
        kind: "page",
        chapter: String(chapter.id),
        label: chapter.label,
        page: page.page,
        total: chapter.page_count,
        url: page.url,
        read: !!page.read,
      });
    });
  });

  var resumeChapter = shell.dataset.resumeChapter;
  var resumePage = parseInt(shell.dataset.resumePage || "0", 10);
  var pages = []; // appended <img> elements, in order
  var read = {};
  var pending = {};
  var writeQueue = Promise.resolve();

  function postJSON(url, body) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    }).then(function (resp) {
      if (!resp.ok) throw new Error("reader write failed");
      return resp.json();
    });
  }

  function queueWrite(fn) {
    writeQueue = writeQueue.then(fn, fn);
    return writeQueue;
  }

  function mark(img) {
    var chapter = img.dataset.chapter;
    var page = parseInt(img.dataset.page || "0", 10);
    var total = parseInt(img.dataset.total || "0", 10);
    var key = chapter + ":" + page;
    if (!chapter || !page || read[key] || pending[key]) return;
    pending[key] = true;
    queueWrite(function () {
      return postJSON("/api/reader/chapters/" + chapter + "/pages", {
        page: page,
        total_pages: total,
      }).then(function () {
        read[key] = true;
        img.classList.add("is-read");
        delete pending[key];
      }).catch(function () {
        delete pending[key];
      });
    });
  }

  function updatePosition() {
    if (!position || pages.length === 0) return;
    var marker = window.innerHeight * 0.45;
    var best = null;
    var bestDistance = Infinity;
    for (var i = 0; i < pages.length; i++) {
      var rect = pages[i].getBoundingClientRect();
      if (rect.height <= 0) continue;
      if (rect.top <= marker && rect.bottom >= marker) {
        best = pages[i];
        break;
      }
      var distance = Math.min(Math.abs(rect.top - marker), Math.abs(rect.bottom - marker));
      if (distance < bestDistance) {
        best = pages[i];
        bestDistance = distance;
      }
    }
    if (best) {
      position.textContent = best.dataset.page + "/" + best.dataset.total;
    }
  }

  var ticking = false;
  function checkVisible() {
    ticking = false;
    updatePosition();
    var threshold = window.innerHeight * 0.85;
    var doc = document.documentElement;
    var atBottom = built && window.scrollY + window.innerHeight >= doc.scrollHeight - 4;
    pages.forEach(function (img) {
      var rect = img.getBoundingClientRect();
      if ((rect.bottom > 0 && rect.bottom <= threshold) || (atBottom && rect.top < window.innerHeight)) {
        mark(img);
      }
    });
  }
  function requestCheck() {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(checkVisible);
  }
  window.addEventListener("scroll", requestCheck, { passive: true });
  window.addEventListener("resize", requestCheck);

  // --- sequential builder: preload a few ahead, append strictly in order ---
  var LOOKAHEAD = 3;
  var loaders = {}; // queue index -> Promise settling when its image is loaded
  var appended = 0;
  var built = false;

  function loaderFor(i) {
    var item = queue[i];
    if (item.kind !== "page") return Promise.resolve(null);
    if (!loaders[i]) {
      loaders[i] = new Promise(function (resolve) {
        var img = new Image();
        img.onload = function () {
          if (img.decode) {
            img.decode().then(function () { resolve(img); }, function () { resolve(img); });
          } else {
            resolve(img);
          }
        };
        img.onerror = function () { resolve(null); };
        img.src = item.url;
      });
    }
    return loaders[i];
  }

  function appendNext() {
    if (appended >= queue.length) {
      built = true;
      if (loadingEl) loadingEl.remove();
      requestCheck();
      return;
    }
    // Warm the pipeline (network only; nothing enters the DOM out of order).
    for (var i = appended; i < Math.min(appended + 1 + LOOKAHEAD, queue.length); i++) {
      loaderFor(i);
    }
    var idx = appended;
    var item = queue[idx];
    if (item.kind === "sep") {
      var sep = document.createElement("div");
      sep.className = "reader-separator";
      sep.textContent = "Chapter " + item.label;
      strip.appendChild(sep);
      appended++;
      appendNext();
      return;
    }
    loaderFor(idx).then(function (img) {
      var el;
      if (img) {
        el = img;
        el.className = "reader-page" + (item.read ? " is-read" : "");
      } else {
        el = document.createElement("div");
        el.className = "reader-page reader-page-failed";
        el.textContent = "Page " + item.page + " failed to load";
      }
      el.id = "chapter-" + item.chapter + "-page-" + item.page;
      el.setAttribute("alt", "Chapter " + item.label + " page " + item.page);
      el.dataset.chapter = item.chapter;
      el.dataset.chapterLabel = item.label;
      el.dataset.page = String(item.page);
      el.dataset.total = String(item.total);
      if (item.read) read[item.chapter + ":" + item.page] = true;
      strip.appendChild(el);
      if (img) pages.push(el);
      delete loaders[idx];
      appended++;
      if (String(item.chapter) === resumeChapter && item.page === resumePage) {
        el.scrollIntoView({ block: "start" });
      }
      requestCheck();
      appendNext();
    });
  }

  appendNext();
})();
