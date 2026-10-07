// keys.js: the editor's first JavaScript island (round 8). It holds the
// hotkeys and their rebind recorder, Ctrl+S without the browser's Save
// dialog, the panel toggles, Ctrl+click and middle-click on tabs, and the
// unsaved-changes guard. It talks to the server only by clicking htmx
// elements, submitting forms and following links; the key map comes from
// <body data-keys>, which the server writes.
(function () {
  "use strict";

  var body = document.body;
  var keymap = {};
  try { keymap = JSON.parse(body.dataset.keys || "{}"); } catch (e) { keymap = {}; }

  // ---- combinations ----

  var keyNames = { ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down", " ": "Space" };
  var modifierKeys = { Control: 1, Alt: 1, Shift: 1, Meta: 1, AltGraph: 1 };

  // comboOf writes a key event the way the server stores a binding:
  // Ctrl, Alt, Shift, then the key, a letter in upper case.
  function comboOf(e) {
    // AltGr arrives as Ctrl+Alt on Windows: it types text (€, [ on many
    // layouts), so it is never a shortcut.
    if (modifierKeys[e.key] || (e.getModifierState && e.getModifierState("AltGraph"))) return null;
    var key = keyNames[e.key] || e.key;
    if (key.length === 1) key = key.toUpperCase();
    var parts = [];
    if (e.ctrlKey) parts.push("Ctrl");
    if (e.altKey) parts.push("Alt");
    if (e.shiftKey) parts.push("Shift");
    parts.push(key);
    return parts.join("+");
  }

  // ---- actions ----

  function click(selector) {
    var el = document.querySelector(selector);
    if (el) el.click();
  }

  function activeTab() { return document.querySelector(".lk-tab.lk-on"); }

  function go(url) {
    leaving = true;
    window.location.assign(url);
  }

  function stepTab(delta) {
    var tab = activeTab();
    if (!tab) return;
    var next = delta < 0 ? tab.previousElementSibling : tab.nextElementSibling;
    if (!next) {
      var all = document.querySelectorAll(".lk-tab");
      next = delta < 0 ? all[all.length - 1] : all[0];
    }
    var link = next && next.querySelector(".lk-tab-name");
    if (link && next !== tab) go(link.href);
  }

  // closeTab closes a tab, asking first when it holds unsaved changes. The
  // draft stays on the server until it is saved or lorekeep stops.
  function closeTab(tab) {
    var link = tab && tab.querySelector("[data-close]");
    if (!link) return;
    if (tab.hasAttribute("data-dirty") &&
        !window.confirm("Close this tab? Its unsaved changes are kept until you save or stop lorekeep.")) {
      return;
    }
    go(link.href);
  }

  function togglePanel(name) {
    var attr = "data-closed-" + name;
    if (body.hasAttribute(attr)) body.removeAttribute(attr);
    else body.setAttribute(attr, "");
  }

  var actions = {
    "switcher": function () { body.dispatchEvent(new CustomEvent("lk-switcher", { bubbles: true })); },
    "save": function () { click("#lk-save"); },
    "close-tab": function () { closeTab(activeTab()); },
    "prev-tab": function () { stepTab(-1); },
    "next-tab": function () { stepTab(1); },
    "toggle-structure": function () { togglePanel("structure"); },
    "toggle-relations": function () { togglePanel("relations"); },
    "toggle-graph": function () { togglePanel("graph"); },
    "fields": function () { click("#lk-fields-toggle"); }
  };

  // ---- the rebind recorder (Keyboard shortcuts page) ----

  var recording = null; // the form whose keys are being recorded

  function startRecording(form) {
    stopRecording();
    recording = form;
    form.setAttribute("data-recording", "");
    var shown = form.querySelector("[data-keys-shown]");
    if (shown) shown.textContent = "press keys…";
  }

  function stopRecording() {
    if (recording) recording.removeAttribute("data-recording");
    recording = null;
  }

  function record(e) {
    if (e.key === "Escape") {
      stopRecording();
      go(window.location.href);
      return;
    }
    var combo = comboOf(e);
    if (!combo) return;
    var form = recording;
    stopRecording();
    form.querySelector("input[name=keys]").value = combo;
    var shown = form.querySelector("[data-keys-shown]");
    if (shown) shown.textContent = combo;
    if (window.htmx) window.htmx.trigger(form, "submit");
  }

  // ---- events ----

  document.addEventListener("keydown", function (e) {
    if (recording) {
      e.preventDefault();
      e.stopPropagation();
      record(e);
      return;
    }
    var combo = comboOf(e);
    var action = combo && keymap[combo];
    if (!action || !actions[action]) return;
    e.preventDefault();
    actions[action]();
  }, true);

  document.addEventListener("click", function (e) {
    var t = e.target.closest("[data-toggle], [data-action], [data-record], [data-close], a[data-new-tab]");
    if (!t) return;
    if (t.hasAttribute("data-toggle")) {
      togglePanel(t.getAttribute("data-toggle"));
    } else if (t.hasAttribute("data-action")) {
      var run = actions[t.getAttribute("data-action")];
      if (run) run();
    } else if (t.hasAttribute("data-record")) {
      startRecording(t.closest("form"));
    } else if (t.hasAttribute("data-close")) {
      e.preventDefault();
      closeTab(t.closest(".lk-tab"));
      return;
    } else if (e.ctrlKey || e.metaKey) {
      // Ctrl+click opens an editor tab, not a browser tab.
      e.preventDefault();
      go(t.getAttribute("data-new-tab"));
      return;
    } else {
      return; // a plain click; the guard below sees it
    }
    e.preventDefault();
  });

  // A middle click on a tab closes it.
  document.addEventListener("auxclick", function (e) {
    if (e.button !== 1) return;
    var tab = e.target.closest(".lk-tab");
    if (!tab) return;
    e.preventDefault();
    closeTab(tab);
  });
  document.addEventListener("mousedown", function (e) {
    if (e.button === 1 && e.target.closest(".lk-tab")) e.preventDefault(); // no autoscroll
  });

  // ---- the unsaved-changes guard ----

  // leaving is set when the editor itself navigates: moving between tabs
  // keeps every draft on the server, so only leaving the editor (closing
  // the window, typing another address) asks first.
  var leaving = false;
  // Only a plain left click navigates this page; a modified click opens
  // another window or tab and this page stays.
  document.addEventListener("click", function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.shiftKey || e.metaKey || e.altKey) return;
    var a = e.target.closest("a[href]");
    if (a && a.origin === window.location.origin && !a.target) leaving = true;
  });
  window.addEventListener("beforeunload", function (e) {
    if (leaving || !document.querySelector("[data-dirty]")) return;
    e.preventDefault();
    e.returnValue = "";
  });
  window.addEventListener("pageshow", function () { leaving = false; });
})();
