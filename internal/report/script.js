
(function () {
  var panel = document.getElementById('panel');
  if (!panel || !document.addEventListener || !panel.closest) { return; }
  var table = document.getElementById('panel-records');
  var label = document.getElementById('panel-label');
  function clear() {
    var old = table.getElementsByTagName('tbody');
    while (old.length !== 0) { table.removeChild(old[0]); }
  }
  function hide() {
    panel.hidden = true;
    document.body.className = '';
  }
  function show(mark) {
    var keys = (mark.getAttribute('data-rec') || '').split(' ');
    var shown = 0;
    clear();
    keys.forEach(function (key) {
      var source = document.getElementById('rec-' + key);
      if (!source) { return; }
      var copy = source.cloneNode(true);
      copy.removeAttribute('id');
      copy.className = copy.className + ' open';
      table.appendChild(copy);
      shown += 1;
    });
    if (shown === 0) { return false; }
    label.textContent = mark.getAttribute('data-label') || '';
    panel.hidden = false;
    panel.scrollTop = 0;
    document.body.className = 'has-panel';
    return true;
  }
  document.addEventListener('click', function (ev) {
    var target = ev.target;
    if (!target || !target.closest) { return; }
    if (target.closest('#panel-close')) { hide(); return; }
    var mark = target.closest('[data-rec]');
    if (mark) {
      if (show(mark)) { ev.preventDefault(); }
      return;
    }
    var id = target.closest('a.recid');
    if (id) {
      if (panel.contains(id)) { hide(); return; }
      var group = id.closest('tbody.rec');
      if (group) {
        ev.preventDefault();
        if (group.className.indexOf(' open') === -1) {
          group.className = group.className + ' open';
        } else {
          group.className = group.className.replace(' open', '');
        }
      }
    }
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape') { hide(); }
  });
})();

(function () {
  /* The card of a stick or a CPU of the board: a copy of it beside the
     pointer, inside the window, while the pointer or the focus is on it. */
  var hover = document.getElementById('mx-hover');
  if (!hover || !document.addEventListener || !hover.closest) { return; }
  var current = null;
  function hide() {
    hover.hidden = true;
    current = null;
    while (hover.firstChild) { hover.removeChild(hover.firstChild); }
  }
  function place(x, y) {
    var w = hover.offsetWidth, h = hover.offsetHeight;
    var left = x + 14, top = y + 14;
    if (left + w > window.innerWidth - 8) { left = Math.max(8, x - w - 14); }
    if (top + h > window.innerHeight - 8) { top = Math.max(8, y - h - 14); }
    hover.style.left = left + 'px';
    hover.style.top = top + 'px';
  }
  function show(mark, x, y) {
    var card = document.getElementById(mark.getAttribute('data-card'));
    if (!card) { return; }
    if (current !== mark) {
      while (hover.firstChild) { hover.removeChild(hover.firstChild); }
      var copy = card.cloneNode(true);
      copy.removeAttribute('id');
      hover.appendChild(copy);
      current = mark;
    }
    hover.hidden = false;
    place(x, y);
  }
  function markOf(t) {
    if (!t) { return null; }
    if (!t.closest) { return null; }
    return t.closest('[data-card]');
  }
  document.addEventListener('mousemove', function (ev) {
    var mark = markOf(ev.target);
    if (mark) { show(mark, ev.clientX, ev.clientY); } else if (current) { hide(); }
  });
  document.addEventListener('focusin', function (ev) {
    var mark = markOf(ev.target);
    if (!mark) { hide(); return; }
    var r = mark.getBoundingClientRect();
    show(mark, r.right, r.top);
  });
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape') { hide(); }
  });
}());

(function () {
  /* The tabs of the Memory Explorer: a link shows its panel in place. */
  var panels = document.querySelector('.mx-panels');
  if (!panels || !document.addEventListener) { return; }
  var tabs = document.querySelectorAll('.mx-tabs a');
  function choose(id) {
    var i;
    for (i = 0; i !== tabs.length; i += 1) {
      tabs[i].className = tabs[i].getAttribute('href') === '#' + id ? 'on' : '';
    }
    var list = panels.querySelectorAll('.mx-panel');
    for (i = 0; i !== list.length; i += 1) {
      list[i].className = list[i].id === id ? 'mx-panel on' : 'mx-panel';
    }
  }
  if (tabs.length === 0) { return; }
  panels.className = 'mx-panels js';
  choose(tabs[0].getAttribute('href').slice(1));
  document.addEventListener('click', function (ev) {
    var a = ev.target.closest ? ev.target.closest('.mx-tabs a') : null;
    if (!a) { return; }
    ev.preventDefault();
    choose(a.getAttribute('href').slice(1));
  });
}());
