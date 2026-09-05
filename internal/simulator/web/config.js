(() => {
  const endpoint = '/public/simulator-configuration';
  const form = document.querySelector('#configuration-form');
  const requires3ds = document.querySelector('#requires-3ds');
  const saveButton = document.querySelector('#save-button');
  const message = document.querySelector('#message');

  function showMessage(text, error = false) {
    message.textContent = text;
    message.classList.toggle('error', error);
  }

  async function request(options) {
    const response = await fetch(endpoint, options);
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || 'The simulator rejected this request.');
    return body;
  }

  function apply(configuration) {
    const provider = configuration.provider === 'barion' ? 'barion' : 'stripe';
    const input = form.querySelector(`input[name="provider"][value="${provider}"]`);
    if (input) input.checked = true;
    requires3ds.checked = configuration.requires3ds === true;
  }

  form.addEventListener('submit', async event => {
    event.preventDefault();
    saveButton.disabled = true;
    showMessage('');
    try {
      const provider = form.querySelector('input[name="provider"]:checked')?.value || 'stripe';
      apply(await request({
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, requires3ds: requires3ds.checked })
      }));
      showMessage('Test configuration saved. New registrations and payments will use this branch.');
    } catch (error) {
      showMessage(error.message || 'Could not save test configuration.', true);
    } finally {
      saveButton.disabled = false;
    }
  });

  request().then(apply).catch(error => showMessage(error.message || 'Could not load test configuration.', true));
})();
