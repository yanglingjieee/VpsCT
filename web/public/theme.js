// Applied before first paint so a dark-mode user never sees a light flash.
// It is a file, not an inline script: the panel's CSP only runs same-origin scripts.
(function () {
  var t = localStorage.getItem("ctlvps-theme");
  var dark = t === "dark" || (t !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  if (dark) document.documentElement.classList.add("dark");
})();
