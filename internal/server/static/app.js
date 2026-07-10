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
  var counts = {};
  var completed = {};

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
    fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    }).catch(function () {});
  }

  function mark(img) {
    var chapter = img.dataset.chapter;
    var page = parseInt(img.dataset.page || "0", 10);
    var total = parseInt(img.dataset.total || "0", 10);
    var key = chapter + ":" + page;
    if (!chapter || !page || read[key]) return;
    read[key] = true;
    counts[chapter] = (counts[chapter] || 0) + 1;
    img.classList.add("is-read");
    postJSON("/api/reader/chapters/" + chapter + "/pages", {
      page: page,
      total_pages: total,
    });
    if (total > 0 && counts[chapter] >= total && !completed[chapter]) {
      completed[chapter] = true;
      postJSON("/api/reader/chapters/" + chapter + "/complete");
    }
  }

  var ticking = false;
  function checkVisible() {
    ticking = false;
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
  window.addEventListener("load", function () {
    var chapter = shell.dataset.resumeChapter;
    var page = shell.dataset.resumePage;
    var target = chapter && page ? document.getElementById("chapter-" + chapter + "-page-" + page) : null;
    if (target) target.scrollIntoView({ block: "start" });
  });

  document.addEventListener("click", function (e) {
    if (e.target.closest(".reader-controls")) return;
    if (window.matchMedia("(max-width: 1024px)").matches) {
      document.body.classList.toggle("controls-on");
    }
  });
})();
