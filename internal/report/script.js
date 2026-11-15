
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
