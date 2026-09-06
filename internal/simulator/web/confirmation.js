(() => {
  const closeButton = document.querySelector('#close-confirmation');
  closeButton?.addEventListener('click', () => {
    if (window.parent !== window) {
      window.parent.postMessage({ type: 'myscoutee:close-payment-confirmation' }, window.location.origin);
      return;
    }
    window.close();
  });
})();
