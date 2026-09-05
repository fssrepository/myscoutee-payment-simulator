(() => {
  const endpoint = '/configuration-session';
  const form = document.querySelector('#configuration-form');
  const requires3ds = document.querySelector('#requires-3ds');
  const saveButton = document.querySelector('#save-button');
  const message = document.querySelector('#message');
  const parentOrigin = (() => {
    try { return new URL(document.referrer).origin; } catch { return '*'; }
  })();
  let currentConfiguration = {
    provider: 'none',
    requires3ds: false,
    connections: {}
  };

  function showMessage(text, error = false) {
    message.textContent = text;
    message.classList.toggle('error', error);
  }

  async function request(options, url = endpoint) {
    const response = await fetch(url, options);
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || 'The simulator rejected this request.');
    return body;
  }

  function connected(provider) {
    return provider === 'none' || configurationConnection(provider).connected === true;
  }

  function configurationConnection(provider) {
    return currentConfiguration.connections?.[provider] || {};
  }

  function selectedProvider() {
    return form.querySelector('input[name="provider"]:checked')?.value || 'none';
  }

  function renderConnectionButtons() {
    form.querySelectorAll('[data-connect-provider]').forEach(button => {
      const provider = button.dataset.connectProvider;
      const connection = configurationConnection(provider);
      const isConnected = connection.connected === true;
      button.disabled = isConnected;
      button.classList.toggle('connected', isConnected);
      button.textContent = isConnected ? '✓ Connected' : 'Generate key';
      button.title = isConnected && connection.credentialMask
        ? `Test credential ${connection.credentialMask}`
        : '';
    });
  }

  function syncControls() {
    const provider = selectedProvider();
    const providerConnected = connected(provider);
    requires3ds.disabled = provider === 'none' || !providerConnected;
    if (requires3ds.disabled) requires3ds.checked = false;
    saveButton.disabled = !providerConnected;
    renderConnectionButtons();
  }

  function apply(configuration) {
    currentConfiguration = {
      provider: ['none', 'stripe', 'barion'].includes(configuration.provider)
        ? configuration.provider
        : 'none',
      requires3ds: configuration.requires3ds === true,
      connections: configuration.connections || {}
    };
    const provider = currentConfiguration.provider;
    const input = form.querySelector(`input[name="provider"][value="${provider}"]`);
    if (input) input.checked = true;
    requires3ds.checked = provider !== 'none' && currentConfiguration.requires3ds;
    syncControls();
    window.parent.postMessage({
      source: 'myscoutee-payment-simulator',
      type: 'configuration',
      provider: connected(provider) ? provider : 'none'
    }, parentOrigin);
  }

  form.addEventListener('change', event => {
    if (event.target?.name !== 'provider') return;
    syncControls();
    if (!connected(event.target.value)) {
      showMessage(`Generate the ${event.target.value === 'stripe' ? 'Stripe' : 'Barion'} test key before saving.`, true);
    } else {
      showMessage('');
    }
  });

  form.querySelectorAll('[data-connect-provider]').forEach(button => {
    button.addEventListener('click', async () => {
      const provider = button.dataset.connectProvider;
      if (!provider || connected(provider)) return;
      button.disabled = true;
      showMessage('Generating the isolated demo credential…');
      try {
        const input = form.querySelector(`input[name="provider"][value="${provider}"]`);
        const configuration = await request(
          { method: 'POST' },
          `/configuration-session/providers/${provider}/connection`
        );
        currentConfiguration.connections = configuration.connections || {};
        if (input) input.checked = true;
        syncControls();
        showMessage(`${provider === 'stripe' ? 'Stripe' : 'Barion'} test connection is ready. Save to activate it.`);
      } catch (error) {
        showMessage(error.message || 'Could not generate the test connection.', true);
        syncControls();
      }
    });
  });

  form.addEventListener('submit', async event => {
    event.preventDefault();
    saveButton.disabled = true;
    showMessage('');
    try {
      const provider = selectedProvider();
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
