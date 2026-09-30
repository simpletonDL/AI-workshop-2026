// Progressive enhancement for the atlas forms: instead of a plain navigation
// the form's GET URL is fetched with an X-Atlas-Progress id while
// /progress?id=<id> is polled to show the current stage and percentage. When
// the page arrives it replaces the document and the URL is updated, so result
// pages stay bookmarkable. Without JavaScript the form submits normally.
(function () {
  "use strict";

  // Pages written by this script share one document: going back or forward
  // must load the page of the new URL.
  window.addEventListener("popstate", function () { location.reload(); });

  var form = document.querySelector("main form[method=get]");
  if (!form || !window.fetch || !window.URLSearchParams || !window.FormData || !history.pushState) {
    return;
  }

  function newID() {
    var bytes = new Uint8Array(16);
    window.crypto.getRandomValues(bytes);
    return Array.prototype.map.call(bytes, function (b) {
      return ("0" + b.toString(16)).slice(-2);
    }).join("");
  }

  function progressBox() {
    var box = document.createElement("div");
    box.className = "progress";
    box.setAttribute("role", "status");
    box.setAttribute("aria-live", "polite");
    box.innerHTML = '<div class="progress-head"><span class="progress-stage"></span>' +
      '<span class="progress-percent"></span></div><progress max="100" value="0"></progress>';
    form.parentNode.insertBefore(box, form.nextSibling);
    var percent = 0;
    return function (stage, p) {
      // Never let the bar go back, e.g. if a stale poll arrives late.
      percent = Math.max(percent, p || 0);
      if (stage) box.querySelector(".progress-stage").textContent = stage;
      box.querySelector(".progress-percent").textContent = percent + "%";
      box.querySelector("progress").value = percent;
    };
  }

  form.addEventListener("submit", function (event) {
    if (!window.crypto || !window.crypto.getRandomValues) return;
    // Named buttons (add/remove a repository) only edit the form.
    if (event.submitter && event.submitter.name) return;
    event.preventDefault();

    var url = form.getAttribute("action") + "?" + new URLSearchParams(new FormData(form)).toString();
    var id = newID();
    Array.prototype.forEach.call(form.querySelectorAll("button"), function (b) { b.disabled = true; });
    var old = document.querySelector(".progress");
    if (old) old.parentNode.removeChild(old);
    var render = progressBox();
    render("Starting…", 0);

    var finished = false;
    function poll() {
      if (finished) return;
      fetch("/progress?id=" + id, { cache: "no-store" })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (p) { if (p && !finished) render(p.stage, p.percent); })
        .catch(function () {})
        .then(function () { if (!finished) setTimeout(poll, 500); });
    }
    setTimeout(poll, 200);

    fetch(url, { headers: { "X-Atlas-Progress": id } })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        finished = true;
        render("Done", 100);
        history.pushState(null, "", url);
        document.open();
        document.write(html);
        document.close();
      })
      .catch(function () {
        // Fall back to a normal navigation.
        finished = true;
        location.href = url;
      });
  });
})();
