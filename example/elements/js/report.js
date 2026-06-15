basecoat.register('report-issue', '#example-report:not([data-report-issue-initialized])', function(el) {
  var form = el.querySelector('[data-report-form]');

  el.querySelector('[data-report-cancel]').addEventListener('click', function() {
    form.reset();
  });

  el.querySelector('[data-report-continue]').addEventListener('click', function() {
    var data = {};
    new FormData(form).forEach(function(value, key) { data[key] = value; });
    window.postJSON('/api/report-issue', data, function(resp) {
      window.toast('info', 'This is an example', 'Submitted: ' + JSON.stringify(data));
    });
  });

  el.dataset.reportIssueInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
