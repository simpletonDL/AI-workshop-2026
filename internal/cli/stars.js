// Banana (star) buttons of the skill cards: the banana is given or taken back
// in place and the "Bananas" list is replaced with the one the server returns.
// Without JavaScript the form posts and the server redirects back to the page.
(function () {
  "use strict";

  if (!window.fetch || !window.FormData || !window.URLSearchParams) return;

  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (!form.classList || !form.classList.contains("banana-form") || form.hasAttribute("data-plain")) return;
    event.preventDefault();
    var button = form.querySelector("button.banana");
    if (button.disabled) return;

    var body = new URLSearchParams(new FormData(form));
    body.set("star", button.value);
    button.disabled = true;
    fetch(form.getAttribute("action"), { method: "POST", body: body, headers: { "X-Atlas-Star": "1" } })
      .then(function (r) {
        if (!r.ok) throw new Error("star failed: " + r.status);
        return r.text();
      })
      .then(function (html) {
        var given = button.value === "1";
        button.value = given ? "0" : "1";
        button.setAttribute("aria-pressed", String(given));
        button.title = given ? "Take the banana back" : "Give it a banana";
        button.classList.remove("wiggle");
        if (given) {
          void button.offsetWidth; // restart the animation
          button.classList.add("wiggle");
        }
        var slot = document.querySelector(".bananas-slot");
        if (slot) slot.innerHTML = html;
        button.disabled = false;
      })
      .catch(function () {
        // A normal post shows the server's error.
        button.disabled = false;
        form.setAttribute("data-plain", "");
        form.requestSubmit ? form.requestSubmit(button) : button.click();
      });
  });
})();
