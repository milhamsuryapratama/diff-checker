// Progress and reasoning for the job page.
//
// This is the only JavaScript in the product. EventSource is a browser API with
// no HTML equivalent, so subscribing to the job stream cannot be done in markup
// alone. Everything else — the report, the findings, the diff highlighting — is
// rendered by Go templates on the server.
//
// Two kinds of event arrive. A "job" event carries a full snapshot, so the
// checklist never has to reconcile state: it re-applies what it was told. A
// "trace" event carries one reasoning line, complete rather than incremental,
// keyed by seq — a seq already on screen replaces that line, a new one appends.
// Sending cumulative text costs a little bandwidth and means a dropped frame
// cannot leave the page permanently wrong.
(function () {
  var root = document.querySelector('[data-job]');
  if (!root) return;

  var jobId = root.getAttribute('data-job');
  var statusLine = document.getElementById('status-line');
  var source = new EventSource('/jobs/' + jobId + '/events');
  var lines = {}; // seq -> element

  function applyStep(step) {
    var li = document.querySelector('.step[data-node="' + step.node + '"]');
    if (!li) return;
    li.className = 'step step-' + step.status;
  }

  function panelFor(node) {
    return document.querySelector('.trace[data-trace="' + node + '"]');
  }

  function applyTrace(entry) {
    var panel = panelFor(entry.node);
    if (!panel) return;

    // Reveal the toggle only once a step has something to show.
    var toggle = document.querySelector('.trace-toggle[data-toggle="' + entry.node + '"]');
    if (toggle) toggle.hidden = false;

    var el = lines[entry.seq];
    if (!el) {
      el = document.createElement('div');
      el.className = 'trace-line trace-' + entry.kind;
      lines[entry.seq] = el;
      panel.appendChild(el);
      // Auto-open while a step is actively thinking, so the reasoning is
      // visible without a click; the user can still collapse it.
      if (!panel.dataset.userToggled) panel.hidden = false;
    }
    el.textContent = entry.text;

    if (!panel.hidden) panel.scrollTop = panel.scrollHeight;
  }

  document.addEventListener('click', function (e) {
    var btn = e.target.closest('.trace-toggle');
    if (!btn) return;
    var panel = panelFor(btn.getAttribute('data-toggle'));
    if (!panel) return;
    panel.hidden = !panel.hidden;
    panel.dataset.userToggled = '1';
  });

  source.onmessage = function (e) {
    var payload;
    try {
      payload = JSON.parse(e.data);
    } catch (err) {
      return;
    }

    if (payload.type === 'trace' && payload.trace) {
      applyTrace(payload.trace);
      return;
    }

    var job = payload.job;
    if (!job) return;
    (job.steps || []).forEach(applyStep);

    if (job.status === 'failed') {
      statusLine.textContent = 'Gagal: ' + (job.error || 'kesalahan tidak diketahui');
      statusLine.className = 'hint failed';
      source.close();
      return;
    }

    if (payload.done) {
      statusLine.textContent = 'Selesai. Membuka laporan…';
      source.close();
      window.location = '/jobs/' + jobId + '/report';
      return;
    }

    statusLine.textContent = 'Memproses…';
  };

  // A dropped connection is not an error worth showing: EventSource reconnects
  // on its own, and the replayed backlog re-establishes the correct state.
  source.onerror = function () {
    if (source.readyState === EventSource.CLOSED) {
      statusLine.textContent = 'Koneksi terputus. Muat ulang halaman untuk melihat status.';
    }
  };
})();
