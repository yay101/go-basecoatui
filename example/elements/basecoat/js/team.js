basecoat.register('team-roles', '#example-team:not([data-team-roles-initialized])', function(el) {
  el.querySelectorAll('.select').forEach(function(sel) {
    sel.addEventListener('change', function(e) {
      var hidden = sel.querySelector('input[type="hidden"]');
      var name = hidden ? hidden.name : sel.id;
      var value = e.detail.value;
      var label = sel.querySelector('.truncate').textContent.trim();
      window.postJSON('/api/team-roles', {member: name, role: value}, function(resp) {
        window.toast('success', resp.title || 'Role updated', 'member="' + name + '", role="' + value + '" (' + label + ')');
      });
    });
  });

  el.dataset.teamRolesInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
