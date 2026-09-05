package simulator

import "html/template"

var paymentWaitTemplate = template.Must(template.New("payment-wait").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Waiting for payment confirmation</title>
<style>:root{color-scheme:light;font-family:Inter,ui-sans-serif,system-ui,sans-serif;color:#172640;background:#eef3f9}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:1.25rem;background:radial-gradient(circle at top right,#dce9ff,transparent 46%),#eef3f9}main{width:min(100%,34rem);display:grid;justify-items:center;gap:1rem;border:1px solid #cdd9e8;border-radius:1.2rem;padding:clamp(1.4rem,6vw,2.4rem);text-align:center;background:rgba(255,255,255,.95);box-shadow:0 1rem 2.4rem rgba(39,61,92,.12)}.provider-logo{width:auto;height:2rem;max-width:8rem;object-fit:contain;justify-self:end}.result-icon{width:3.8rem;height:3.8rem;display:grid;place-items:center;border:.35rem solid #d6e2f2;border-top-color:#376cb1;border-radius:50%;font-size:0;font-weight:900;animation:spin .9s linear infinite}main.is-result .result-icon{border-color:currentColor;font-size:2rem;animation:result-in .35s ease-out both}main.is-success .result-icon{color:#24865e;background:#eaf8f1}main.is-failure .result-icon{color:#b83a42;background:#fff0f1}main.is-timeout .result-icon{color:#a76419;background:#fff5e4}h1{margin:.2rem 0 0;font-size:clamp(1.35rem,5vw,1.9rem)}p{margin:0;color:#53657e;line-height:1.55}.reference{font:700 .76rem ui-monospace,SFMono-Regular,monospace;color:#718197;overflow-wrap:anywhere}@keyframes spin{to{transform:rotate(360deg)}}@keyframes result-in{0%{transform:scale(.55);opacity:0}70%{transform:scale(1.12);opacity:1}100%{transform:scale(1)}}@media(prefers-reduced-motion:reduce){.result-icon{animation-duration:2.4s}main.is-result .result-icon{animation:none}}</style></head>
<body><main id="payment-wait" data-id="{{.ID}}" data-status-url="{{.StatusURL}}"><img class="provider-logo" src="/simulator-ui/{{.ProviderSlug}}.svg" alt="{{.Provider}}"><span class="result-icon" aria-hidden="true"></span><h1>Waiting for external confirmation</h1><p id="wait-message">Confirm in the external simulator application. Time remaining: <strong id="countdown">3:00</strong>.</p><span class="reference">{{.Reference}}</span></main><script src="/simulator-ui/payment-wait.js" defer></script></body></html>`))

var paymentMethodRegistrationAuthorizationTemplate = template.Must(template.New("payment-method-registration-auth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Card registration confirmation</title>
<style>:root{color-scheme:light;font-family:Inter,ui-sans-serif,system-ui,sans-serif;color:#172640;background:#eef3f9}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:1.25rem;background:radial-gradient(circle at top right,#dce9ff,transparent 46%),#eef3f9}main{width:min(100%,36rem);border:2px solid #526987;border-radius:1rem;padding:1.5rem;background:#fff;box-shadow:0 1rem 2.4rem rgba(39,61,92,.12)}.provider-logo{display:block;width:auto;height:2rem;max-width:8rem;margin:0 0 1rem auto;object-fit:contain}.warning{color:#8a3d00}.details{line-height:1.7;color:#53657e}.actions{display:grid;gap:.7rem;margin-top:1.4rem}button{width:100%;padding:.8rem;border:0;border-radius:.6rem;background:#236b47;color:white;font-weight:700}.danger{background:#a63333}.status{font-weight:800;text-transform:capitalize}</style></head>
<body><main><img class="provider-logo" src="/simulator-ui/{{.ProviderSlug}}.svg" alt="{{.Provider}}">
{{if .Pending}}
<h1>Confirm card registration</h1><p class="warning">Simulated 3DS confirmation for saving this card. No payment is created.</p>
<p class="details"><strong>Member:</strong> {{.Cardholder}}<br><strong>Card:</strong> •••• {{.Last4}}<br><strong>Reference:</strong> {{.ID}}</p>
<div class="actions">
<form method="post" action="/test/payment-method-registrations/{{.ID}}/approve?token={{.Token}}"><button>Approve card registration</button></form>
<form method="post" action="/test/payment-method-registrations/{{.ID}}/decline?token={{.Token}}"><button class="danger">Decline card registration</button></form>
</div>
{{else}}
<h1>Card registration result</h1><p>Status: <span class="status">{{.Status}}</span></p>
{{end}}
</main></body></html>`))
