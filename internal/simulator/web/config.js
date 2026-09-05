(() => {
  const endpoint = '/configuration-session';
  const form = document.querySelector('#configuration-form');
  const requires3ds = document.querySelector('#requires-3ds');
  const saveButton = document.querySelector('#save-button');
  const message = document.querySelector('#message');
  const activeProviderLogo = document.querySelector('#active-provider-logo');
  const parentOrigin = (() => {
    try { return new URL(document.referrer).origin; } catch { return '*'; }
  })();
  let currentConfiguration = {
    provider: 'none',
    requires3ds: false
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

  function selectedProvider() {
    return form.querySelector('input[name="provider"]:checked')?.value || 'none';
  }

  function syncControls() {
    const provider = selectedProvider();
    requires3ds.disabled = provider === 'none';
    if (requires3ds.disabled) requires3ds.checked = false;
    const providerPresentation = {
      none: { asset: 'cash-only.svg', label: 'Cash only' },
      stripe: { asset: 'stripe.svg', label: 'Stripe' },
      barion: { asset: 'barion.svg', label: 'Barion' }
    }[provider];
    activeProviderLogo.src = `/simulator-ui/${providerPresentation.asset}`;
    activeProviderLogo.alt = providerPresentation.label;
  }

  function apply(configuration) {
    currentConfiguration = {
      provider: ['none', 'stripe', 'barion'].includes(configuration.provider)
        ? configuration.provider
        : 'none',
      requires3ds: configuration.requires3ds === true
    };
    const provider = currentConfiguration.provider;
    const input = form.querySelector(`input[name="provider"][value="${provider}"]`);
    if (input) input.checked = true;
    requires3ds.checked = provider !== 'none' && currentConfiguration.requires3ds;
    syncControls();
    window.parent.postMessage({
      source: 'myscoutee-payment-simulator',
      type: 'configuration',
      provider
    }, parentOrigin);
  }

  form.addEventListener('change', event => {
    if (event.target?.name !== 'provider') return;
    syncControls();
    showMessage('');
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
      showMessage(provider === 'none'
        ? 'Cash-only test configuration saved.'
        : 'Test configuration saved. New registrations and payments will use this provider.');
    } catch (error) {
      showMessage(error.message || 'Could not save test configuration.', true);
    } finally {
      saveButton.disabled = false;
    }
  });

  request().then(apply).catch(error => showMessage(error.message || 'Could not load test configuration.', true));
})();
