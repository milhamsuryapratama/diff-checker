// Progress updates for the job page.
//
// This is the only JavaScript in the product. EventSource is a browser API with
// no HTML equivalent, so subscribing to the job stream cannot be done in markup
// alone. Everything else — the report, the findings, the diff highlighting — is
// rendered by Go templates on the server.
//
// The server sends a full job snapshot on every event rather than a delta, so
// this code never has to reconcile state: it just re-applies what it was told.
(function () {
  var root = document.querySelector('[data-job]');
  if (!root) return;

  var jobId = root.getAttribute('data-job');
  var statusLine = document.getElementById('status-line');
  var source = new EventSource('/jobs/' + jobId + '/events');

  function applyStep(step) {
    var li = document.querySelector('.step[data-node="' + step.node + '"]');
    if (!li) return;
    li.className = 'step step-' + step.status;
  }

  source.onmessage = function (e) {
    var payload;
    try {
      payload = JSON.parse(e.data);
    } catch (err) {
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
  // on its own, and the next snapshot re-establishes the correct state.
  source.onerror = function () {
    if (source.readyState === EventSource.CLOSED) {
      statusLine.textContent = 'Koneksi terputus. Muat ulang halaman untuk melihat status.';
    }
  };
})();
