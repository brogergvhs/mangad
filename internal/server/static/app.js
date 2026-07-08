// Collapsible table rows: a .row-toggle reveals the detail row it points at.
document.addEventListener("click", function (e) {
  var toggle = e.target.closest(".row-toggle");
  if (!toggle) return;
  var target = document.getElementById(toggle.dataset.target);
  if (!target) return;
  target.hidden = !target.hidden;
  toggle.classList.toggle("open");
});
