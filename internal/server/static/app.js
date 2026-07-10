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

  var pages = Array.prototype.slice.call(document.querySelectorAll(".reader-page"));
  var read = {};
  var pending = {};
  var counts = {};
  var position = document.getElementById("reader-position");
  var prev = document.querySelector("[data-reader-prev]");
  var next = document.querySelector("[data-reader-next]");
  var writeQueue = Promise.resolve();

  pages.forEach(function (img) {
    var chapter = img.dataset.chapter;
    var key = chapter + ":" + img.dataset.page;
    counts[chapter] = counts[chapter] || 0;
    if (img.dataset.read === "true") {
      read[key] = true;
      counts[chapter]++;
    }
  });

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
        counts[chapter] = (counts[chapter] || 0) + 1;
        img.classList.add("is-read");
        delete pending[key];
      }).catch(function () {
        delete pending[key];
      });
    });
  }

  function updatePosition() {
    if (!position || pages.length === 0) return;
    var best = pages[0];
    var marker = window.innerHeight * 0.45;
    var bestDistance = Infinity;
    pages.forEach(function (img) {
      var rect = img.getBoundingClientRect();
      if (rect.top <= marker && rect.bottom >= marker) {
        best = img;
        bestDistance = 0;
      } else if (bestDistance !== 0 && rect.bottom >= 0 && rect.top <= window.innerHeight) {
        var distance = Math.min(Math.abs(rect.top - marker), Math.abs(rect.bottom - marker));
        if (distance < bestDistance) {
          best = img;
          bestDistance = distance;
        }
      }
    });
    position.textContent = best.dataset.page + "/" + best.dataset.total;
  }

  function preloadFrom(start) {
    for (var i = start; i < Math.min(start + 4, pages.length); i++) {
      var img = new Image();
      img.src = pages[i].src;
    }
  }

  var ticking = false;
  function checkVisible() {
    ticking = false;
    updatePosition();
    var threshold = window.innerHeight * 0.85;
    var atBottom = window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 4;
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
  pages.forEach(function (img) {
    img.addEventListener("load", requestCheck, { once: true });
  });
  window.addEventListener("load", function () {
    var chapter = shell.dataset.resumeChapter;
    var page = shell.dataset.resumePage;
    var target = chapter && page ? document.getElementById("chapter-" + chapter + "-page-" + page) : null;
    if (target) {
      target.scrollIntoView({ block: "start" });
      preloadFrom(Math.max(0, pages.indexOf(target) + 1));
    }
    requestCheck();
  });

  document.addEventListener("click", function (e) {
    if (e.target.closest(".reader-controls")) return;
    if (window.matchMedia("(max-width: 1024px)").matches) {
      document.body.classList.toggle("controls-on");
    }
  });

  document.addEventListener("keydown", function (e) {
    if (e.target.closest("input, textarea, select")) return;
    if (!window.matchMedia("(min-width: 761px)").matches) return;
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
})();
