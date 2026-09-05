package simulator

import "html/template"

var checkoutTemplate = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MyScoutee payment simulator</title>
<style>body{font:16px system-ui;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#18253a}main{border:1px solid #ccd6e4;border-radius:1rem;padding:1.5rem;background:#f8fafc}.warning{color:#8a3d00}.actions{display:grid;gap:.7rem;margin-top:1.4rem}button{width:100%;padding:.8rem;border:0;border-radius:.6rem;background:#315da8;color:white;font-weight:700}.secondary{background:#596579}.danger{background:#a63333}</style></head>
<body><main><h1>Test checkout</h1><p class="warning">Simulator only. Never enter real card data.</p>
<p><strong>Session:</strong> {{.Session.ID}}<br><strong>Total:</strong> {{.Session.AmountTotal}} {{.Session.Currency}} minor units<br><strong>Status:</strong> {{.Session.PaymentStatus}}</p>
<div class="actions">
<form method="post" action="/test/sessions/{{.Session.ID}}/authorize?token={{.Token}}"><button>Complete checkout</button></form>
<form method="post" action="/test/sessions/{{.Session.ID}}/3ds?token={{.Token}}"><button>Continue with bank authentication (3DS)</button></form>
<form method="post" action="/test/sessions/{{.Session.ID}}/fail?token={{.Token}}"><button class="danger">Fail checkout</button></form>
<form method="post" action="/test/sessions/{{.Session.ID}}/expire?token={{.Token}}"><button class="danger">Expire session</button></form>
<form method="post" action="/test/sessions/{{.Session.ID}}/cancel?token={{.Token}}"><button class="secondary">Cancel and return</button></form>
</div></main></body></html>`))

var bankAuthTemplate = template.Must(template.New("bank-auth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Simulated bank authentication</title>
<style>body{font:16px system-ui;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#18253a}main{border:2px solid #526987;border-radius:1rem;padding:1.5rem;background:#eef3f9}.warning{color:#8a3d00}.actions{display:grid;gap:.7rem;margin-top:1.4rem}button{width:100%;padding:.8rem;border:0;border-radius:.6rem;background:#236b47;color:white;font-weight:700}.secondary{background:#596579}.danger{background:#a63333}</style></head>
<body><main><h1>Was this you?</h1><p class="warning">Simulated bank confirmation. MyScoutee does not control whether this challenge is required.</p>
<p><strong>Payment intent:</strong> {{.Intent.ID}}<br><strong>Total:</strong> {{.Intent.Amount}} {{.Intent.Currency}} minor units<br><strong>Status:</strong> {{.Intent.Status}}</p>
<div class="actions">
<form method="post" action="/test/bank-auth/{{.Session.ID}}/approve?token={{.Token}}"><button>Yes, it was me</button></form>
<form method="post" action="/test/bank-auth/{{.Session.ID}}/decline?token={{.Token}}"><button class="danger">No, it wasn't me</button></form>
</div></main></body></html>`))
