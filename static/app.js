// Tiny vanilla-JS glue for the one thing HTML/HTMX genuinely can't do on
// its own: writing to the clipboard. No framework, no build step.
document.addEventListener("click", function (event) {
  var btn = event.target.closest("[data-copy]");
  if (!btn) return;

  var value = btn.getAttribute("data-copy");
  navigator.clipboard.writeText(value).then(function () {
    var original = btn.textContent;
    btn.textContent = "Copié ✓";
    setTimeout(function () {
      btn.textContent = original;
    }, 1500);
  });
});
