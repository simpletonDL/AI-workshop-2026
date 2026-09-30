// Repository list of the atlas forms: adds and removes rows in place. Without
// JavaScript the add/remove buttons submit the form and the server returns it
// edited. Pasting several lines ("<url>" or "<url> <ref>") into a URL field
// fills one row per line.
(function () {
  "use strict";

  var list = document.querySelector(".repo-rows");
  if (!list || !list.closest) return;
  var form = list.closest("form");
  var add = form.querySelector("button[name=add]");
  var max = parseInt(list.getAttribute("data-max"), 10) || 10;

  function rows() { return list.querySelectorAll(".repo-row"); }
  function field(row, name) { return row.querySelector("input[name=" + name + "]"); }

  // Keeps the remove indexes (used without JavaScript) and the add button in sync.
  function update() {
    var all = rows();
    for (var i = 0; i < all.length; i++) {
      all[i].querySelector("button[name=remove]").value = i;
    }
    if (add) add.disabled = all.length >= max;
  }

  function addRow() {
    var all = rows();
    if (all.length >= max) return null;
    var row = all[0].cloneNode(true);
    field(row, "repo").value = "";
    field(row, "ref").value = "";
    field(row, "repo").removeAttribute("autofocus");
    list.appendChild(row);
    update();
    return row;
  }

  list.addEventListener("click", function (event) {
    var button = event.target.closest("button[name=remove]");
    if (!button) return;
    event.preventDefault();
    var row = button.closest(".repo-row");
    var all = rows();
    if (all.length > 1) {
      var next = row.nextElementSibling || row.previousElementSibling;
      list.removeChild(row);
      field(next, "repo").focus();
    } else {
      // The last row is cleared, so there is always a field to type into.
      field(row, "repo").value = "";
      field(row, "ref").value = "";
      field(row, "repo").focus();
    }
    update();
  });

  if (add) {
    add.addEventListener("click", function (event) {
      event.preventDefault();
      var row = addRow();
      if (row) field(row, "repo").focus();
    });
  }

  list.addEventListener("paste", function (event) {
    var input = event.target;
    if (input.name !== "repo" || !event.clipboardData) return;
    var lines = event.clipboardData.getData("text").split(/\r?\n/).map(function (l) {
      return l.trim();
    }).filter(Boolean);
    if (lines.length < 2) return;
    event.preventDefault();
    var row = input.closest(".repo-row");
    for (var i = 0; i < lines.length && row; i++) {
      var fields = lines[i].split(/\s+/);
      field(row, "repo").value = fields[0];
      field(row, "ref").value = fields[1] || "";
      if (i + 1 < lines.length) row = addRow();
    }
  });

  update();
})();
