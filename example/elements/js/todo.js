basecoat.register('todo', '#todo-app:not([data-todo-initialized])', function(el) {
  var input  = el.querySelector('input');
  var list   = el.querySelector('ul');
  var form   = el.querySelector('form');
  var tpl    = document.getElementById('todo-item');

  form.addEventListener('submit', function(e) {
    e.preventDefault();
    var text = input.value.trim();
    if (!text) return;
    var li = document.importNode(tpl.content, true).firstElementChild;
    li.querySelector('span').textContent = text;
    li.querySelector('button').addEventListener('click', function() { li.remove(); });
    list.appendChild(li);
    input.value = '';
    input.focus();
  });

  el.dataset.todoInitialized = '';
  el.dispatchEvent(new CustomEvent('basecoat:initialized'));
});
