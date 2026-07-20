// dropdowns: close any open <details.dropdown> on outside click or action
document.addEventListener("click", function (e) {
  var inDrop = e.target.closest("details.dropdown");
  document.querySelectorAll("details.dropdown[open]").forEach(function (d) {
    if (d !== inDrop || e.target.closest(".menu a, .menu button, .dropdown-content button")) {
      d.removeAttribute("open");
    }
  });
});

// close the mobile drawer after navigating or acting from within it
document.addEventListener("click", function (e) {
  if (!e.target.closest(".drawer-side .menu a")) return;
  var toggle = document.getElementById("app-drawer");
  if (toggle && window.matchMedia("(max-width: 1023px)").matches) toggle.checked = false;
});

function showErrorToast(msg) {
  var toast = document.getElementById("toast");
  if (!toast) return;
  toast.innerHTML = '<div class="alert alert-error shadow-lg"><span></span></div>';
  toast.querySelector("span").textContent = msg;
}

document.body.addEventListener("htmx:sendError", function () {
  showErrorToast("Network error. Check the server connection.");
});
document.body.addEventListener("htmx:timeout", function () {
  showErrorToast("Request timed out. Try again.");
});
document.body.addEventListener("htmx:responseError", function (e) {
  if (e.detail.xhr && e.detail.xhr.status >= 500) {
    showErrorToast("Server error. Check the logs and try again.");
  }
});

// confirm modal: replaces native hx-confirm (browsers can suppress the
// native dialog); renders an in-app daisyUI modal instead
document.addEventListener("htmx:confirm", function (e) {
  var question = e.detail.question;
  if (!question) return;
  e.preventDefault();
  var dlg = document.createElement("dialog");
  dlg.className = "modal modal-open";
  dlg.innerHTML =
    '<div class="modal-box"><p class="py-2"></p>' +
    '<div class="modal-action"><button class="btn btn-ghost" data-cancel>Cancel</button>' +
    '<button class="btn btn-error" data-ok>Confirm</button></div></div>' +
    '<div class="modal-backdrop" data-cancel></div>';
  dlg.querySelector("p").textContent = question;
  function close() { dlg.remove(); document.removeEventListener("keydown", onKey); }
  function onKey(ev) { if (ev.key === "Escape") close(); }
  dlg.addEventListener("click", function (ev) {
    if (ev.target.closest("[data-cancel]")) { close(); return; }
    if (ev.target.closest("[data-ok]")) { close(); e.detail.issueRequest(true); }
  });
  document.addEventListener("keydown", onKey);
  document.body.appendChild(dlg);
  dlg.querySelector("[data-ok]").focus();
});

// Collapsible table rows: a .row-toggle reveals the detail row it points at.
// Clicks on interactive elements inside the row are ignored. Open rows are
// remembered so frequent table refreshes (polling) don't collapse them.
var openDetails = new Set();
document.addEventListener("click", function (e) {
  if (e.target.closest("button, a, input, select, textarea, form, label, details, summary")) return;
  var toggle = e.target.closest(".row-toggle");
  if (!toggle) return;
  var target = document.getElementById(toggle.dataset.target);
  if (!target) return;
  target.hidden = !target.hidden;
  toggle.classList.toggle("open", !target.hidden);
  if (target.hidden) {
    openDetails.delete(target.id);
  } else {
    openDetails.add(target.id);
  }
});
document.body.addEventListener("htmx:afterSwap", function () {
  openDetails.forEach(function (id) {
    var el = document.getElementById(id);
    if (!el) return;
    el.hidden = false;
    var toggle = document.querySelector('[data-target="' + id + '"]');
    if (toggle) toggle.classList.add("open");
  });
});

// Tag picker: the filter input hides non-matching checkbox rows.
document.addEventListener("input", function (e) {
  var filter = e.target.closest("[data-tag-filter]");
  if (!filter) return;
  var list = filter.parentElement.querySelector("[data-tag-list]");
  if (!list) return;
  var q = filter.value.trim().toLowerCase();
  list.querySelectorAll("label").forEach(function (row) {
    row.hidden = q !== "" && row.textContent.toLowerCase().indexOf(q) === -1;
  });
  var divider = list.querySelector("[data-tag-divider]");
  if (divider) divider.hidden = q !== "";
});

// Tag picker: keep the "N selected" summary in sync with the checkboxes.
document.addEventListener("change", function (e) {
  if (!e.target.matches("[data-tag-list] input[type='checkbox']")) return;
  var details = e.target.closest("details.dropdown");
  if (!details) return;
  var summary = details.querySelector("[data-tag-count]");
  if (!summary) return;
  var count = details.querySelectorAll("[data-tag-list] input:checked").length;
  summary.textContent = count ? count + " selected" : "None selected";
});

function updateLibraryBulk() {
  var form = document.getElementById("library-bulk");
  if (!form) return;
  var seen = {};
  var count = 0;
  document.querySelectorAll("[data-bulk-title]:checked").forEach(function (c) {
    if (seen[c.value]) return;
    seen[c.value] = true;
    count++;
  });
  form.hidden = count === 0;
  var button = form.querySelector("[data-bulk-apply]");
  if (button) button.textContent = "Apply to " + count + " selected title" + (count === 1 ? "" : "s");
}
document.addEventListener("change", function (e) {
  if (e.target.matches("[data-bulk-title]")) updateLibraryBulk();
});
document.body.addEventListener("htmx:afterSwap", updateLibraryBulk);
updateLibraryBulk();

// Library filters: mirror table refreshes into the URL so reloads and
// back-navigation keep them; plain sidebar links reset them.
document.body.addEventListener("htmx:afterRequest", function (e) {
  if (!e.detail.successful || !e.detail.xhr || !e.detail.xhr.responseURL) return;
  if (e.detail.elt && e.detail.elt.hasAttribute("data-poll")) return;
  var url;
  try { url = new URL(e.detail.xhr.responseURL); } catch (err) { return; }
  if (url.pathname !== "/ui/library/table") return;
  var current = new URLSearchParams(location.search);
  ["new-screen", "edit"].forEach(function (key) {
    if (current.has(key)) url.searchParams.set(key, current.get(key));
  });
  var qs = url.searchParams.toString();
  history.replaceState(null, "", "/library" + (qs ? "?" + qs : ""));
});

// Search filters: mirror the form state into the /search URL.
document.body.addEventListener("htmx:afterRequest", function (e) {
  if (location.pathname !== "/search") return;
  if (!e.detail.successful || !e.detail.xhr || !e.detail.xhr.responseURL) return;
  var path;
  try { path = new URL(e.detail.xhr.responseURL).pathname; } catch (err) { return; }
  if (path !== "/ui/search" && path !== "/ui/search/trending") return;
  var form = document.getElementById("search-form");
  if (!form) return;
  var qs = new URLSearchParams(new FormData(form)).toString();
  history.replaceState(null, "", qs ? "/search?" + qs : "/search");
});

// Sidebar screen reordering: drag rows, persist the new order on drop.
(function () {
  var list = document.querySelector("[data-screen-list]");
  if (!list) return;
  var dragging = null;
  list.addEventListener("dragstart", function (e) {
    dragging = e.target.closest("[data-screen-id]");
    if (dragging) dragging.classList.add("dragging");
  });
  list.addEventListener("dragend", function () {
    if (dragging) dragging.classList.remove("dragging");
    dragging = null;
  });
  list.addEventListener("dragover", function (e) {
    if (!dragging) return;
    e.preventDefault();
    var over = e.target.closest("[data-screen-id]");
    if (!over || over === dragging) return;
    var rect = over.getBoundingClientRect();
    var after = e.clientY > rect.top + rect.height / 2;
    list.insertBefore(dragging, after ? over.nextSibling : over);
  });
  list.addEventListener("drop", function (e) {
    e.preventDefault();
    var params = new URLSearchParams();
    list.querySelectorAll("[data-screen-id]").forEach(function (row) {
      params.append("order", row.dataset.screenId);
    });
    fetch("/ui/screens/reorder", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: params.toString(),
    });
  });
})();

(function () {
  var shell = document.querySelector("[data-reader]");
  if (!shell) return;
  var manifestTag = document.getElementById("reader-manifest");
  var strip = document.getElementById("reader-strip");
  var loadingEl = document.getElementById("reader-loading");
  var position = document.getElementById("reader-position");
  var prev = document.querySelector("[data-reader-prev]");
  var next = document.querySelector("[data-reader-next]");

  // controls & keyboard work even on the empty page
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
      goNext();
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
  var seenChapters = {};
  var lastChapterID = 0;
  function enqueueChapters(chapters) {
    var added = 0;
    (chapters || []).forEach(function (chapter) {
      if (seenChapters[chapter.id]) return;
      seenChapters[chapter.id] = true;
      lastChapterID = chapter.id;
      added++;
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
    return added;
  }
  enqueueChapters(manifest.chapters);

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
      keepalive: true, // final page marks must survive chapter navigation
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
      return postJSON((manifest.mark_base || "/api/reader/chapters/") + chapter + "/pages", {
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
      // Anything whose bottom rose above the 85% line counts as read — with
      // no lower bound, so short pages that jump across the whole band (or
      // off-screen entirely) between two scroll frames aren't skipped.
      if (rect.bottom <= threshold || (atBottom && rect.top < window.innerHeight)) {
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

  // Moving on to the next chapter finishes the current screen: short final
  // pages sit fully visible without ever crossing the 85% line, so sweep
  // everything on or above the viewport, flush the write queue, then follow
  // the link (keepalive covers writes the browser would otherwise cancel).
  function goNext() {
    if (!next) return;
    if (!pages) { // the empty page initialises no strip state
      location.href = next.href;
      return;
    }
    pages.forEach(function (img) {
      if (img.getBoundingClientRect().top < window.innerHeight) mark(img);
    });
    queueWrite(function () { location.href = next.href; });
  }
  if (next) {
    next.addEventListener("click", function (e) {
      e.preventDefault();
      goNext();
    });
  }

  // sequential builder: preload a few ahead, append strictly in order.
  // Appending is gated on scroll proximity so a long title isn't downloaded
  // in the background, and the strip keeps extending with the next chapter
  // as the reader approaches the end — no need to reopen the reader.
  var LOOKAHEAD = 3;
  var GATE_PX = window.innerHeight * 2.5;
  var loaders = {}; // queue index -> Promise settling when its image is loaded
  var appended = 0;
  var built = false;
  var waiting = false;   // paused until the reader scrolls closer to the end
  var fetching = false;  // a manifest extension request is in flight
  var noMore = false;    // the server has no further chapters
  var resumed = !resumeChapter || !resumePage;

  function nearEnd() {
    var doc = document.documentElement;
    return doc.scrollHeight - (window.scrollY + window.innerHeight) < GATE_PX;
  }

  function extend() {
    if (fetching || noMore || !lastChapterID) return finish();
    fetching = true;
    fetch(manifest.extend_base ? manifest.extend_base + lastChapterID : "/api/reader/titles/" + manifest.title_id + "/manifest?chapter=" + lastChapterID)
      .then(function (resp) { if (!resp.ok) throw new Error("manifest fetch failed"); return resp.json(); })
      .then(function (m) {
        fetching = false;
        if (enqueueChapters(m.chapters) > 0) {
          appendNext();
        } else {
          noMore = true;
          finish();
        }
      })
      .catch(function () { fetching = false; noMore = true; finish(); });
  }

  function finish() {
    built = true;
    if (loadingEl) loadingEl.remove();
    requestCheck();
  }

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
      extend();
      return;
    }
    // Past the resume point, only build while the reader is near the end of
    // the strip; the scroll handler resumes the pipeline.
    if (resumed && !nearEnd()) {
      waiting = true;
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
      pages.push(el); // failed pages count too, else one broken image blocks completion + resume forever
      delete loaders[idx];
      appended++;
      if (!resumed && String(item.chapter) === resumeChapter && item.page === resumePage) {
        el.scrollIntoView({ block: "start" });
        resumed = true;
      }
      requestCheck();
      appendNext();
    });
  }

  window.addEventListener("scroll", function () {
    if (waiting && nearEnd()) {
      waiting = false;
      appendNext();
    }
  }, { passive: true });

  appendNext();
})();
