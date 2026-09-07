(() => {
  const closeButton = document.querySelector('#close-confirmation');
  function closeConfirmation() {
    if (window.parent !== window) {
      window.parent.postMessage({ type: 'myscoutee:close-payment-confirmation' }, window.location.origin);
      return;
    }
    window.close();
  }

  closeButton?.addEventListener('click', closeConfirmation);
  if (document.querySelector('[data-confirmation-complete="true"]')) {
    window.setTimeout(closeConfirmation, 900);
  }
})();
