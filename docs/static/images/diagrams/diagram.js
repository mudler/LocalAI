// Picks the colour theme for a diagram page. The render script opens a page
// with ?theme=dark or ?theme=light. Without a query, the page follows the
// colour scheme of the browser (see diagram.css).
(function () {
  var m = /[?&]theme=(light|dark)/.exec(location.search);
  if (m) document.documentElement.setAttribute("data-theme", m[1]);
})();
