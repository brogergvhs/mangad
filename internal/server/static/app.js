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
