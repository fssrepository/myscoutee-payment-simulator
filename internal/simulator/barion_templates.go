package simulator

import "html/template"

var barionGatewayTemplate = template.Must(template.New("barion-gateway").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Barion Smart Gateway simulator</title>
<style>body{font:16px system-ui;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#18253a}main{border:2px solid #64b32c;border-radius:1rem;padding:1.5rem;background:#f6fbf2}.warning{color:#8a3d00}.actions{display:grid;gap:.7rem;margin-top:1.4rem}button{width:100%;padding:.8rem;border:0;border-radius:.6rem;background:#4b8d22;color:white;font-weight:700}.secondary{background:#596579}.danger{background:#a63333}</style></head>
<body><main><h1>Barion Smart Gateway simulator</h1><p class="warning">QA only. Never enter real card or wallet data.</p>
<p><strong>Payment:</strong> {{.Payment.PaymentID}}<br><strong>Total:</strong> {{.Payment.Total}} {{.Payment.Currency}}<br><strong>Type:</strong> {{.Payment.PaymentType}}<br><strong>Status:</strong> {{.Payment.Status}}</p>
<div class="actions">
<form method="post" action="/test/barion/payments/{{.Payment.PaymentID}}/authorize?token={{.Token}}"><button>Authorize without visible challenge</button></form>
<form method="post" action="/test/barion/payments/{{.Payment.PaymentID}}/3ds?token={{.Token}}"><button>Continue with bank authentication (3DS)</button></form>
<form method="post" action="/test/barion/payments/{{.Payment.PaymentID}}/cancel?token={{.Token}}"><button class="secondary">Cancel payment</button></form>
<form method="post" action="/test/barion/payments/{{.Payment.PaymentID}}/expire?token={{.Token}}"><button class="danger">Expire payment</button></form>
</div></main></body></html>`))

var barionBankAuthTemplate = template.Must(template.New("barion-bank-auth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Barion simulated bank authentication</title>
<style>body{font:16px system-ui;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#18253a}main{border:2px solid #526987;border-radius:1rem;padding:1.5rem;background:#eef3f9}.warning{color:#8a3d00}.actions{display:grid;gap:.7rem;margin-top:1.4rem}button{width:100%;padding:.8rem;border:0;border-radius:.6rem;background:#236b47;color:white;font-weight:700}.secondary{background:#596579}.danger{background:#a63333}</style></head>
<body><main><h1>Was this you?</h1><p class="warning">Simulated bank confirmation. MyScoutee does not control whether this challenge is required.</p>
<p><strong>Payment:</strong> {{.Payment.PaymentID}}<br><strong>Total:</strong> {{.Payment.Total}} {{.Payment.Currency}}<br><strong>Status:</strong> {{.Payment.Status}}</p>
<div class="actions">
<form method="post" action="/test/barion/bank-auth/{{.Payment.PaymentID}}/approve?token={{.Token}}"><button>Yes, it was me</button></form>
<form method="post" action="/test/barion/bank-auth/{{.Payment.PaymentID}}/decline?token={{.Token}}"><button class="danger">No, it wasn't me</button></form>
</div></main></body></html>`))
