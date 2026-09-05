package simulator

import "html/template"

var paymentWaitTemplate = template.Must(template.New("payment-wait").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Waiting for payment confirmation</title>
<style>:root{color-scheme:light;font-family:Inter,ui-sans-serif,system-ui,sans-serif;color:#172640;background:#eef3f9}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:1.25rem;background:radial-gradient(circle at top right,#dce9ff,transparent 46%),#eef3f9}main{width:min(100%,34rem);display:grid;justify-items:center;gap:1rem;border:1px solid #cdd9e8;border-radius:1.2rem;padding:clamp(1.4rem,6vw,2.4rem);text-align:center;background:rgba(255,255,255,.95);box-shadow:0 1rem 2.4rem rgba(39,61,92,.12)}.ring{width:3.4rem;height:3.4rem;border:.35rem solid #d6e2f2;border-top-color:#376cb1;border-radius:50%;animation:spin .9s linear infinite}h1{margin:.2rem 0 0;font-size:clamp(1.35rem,5vw,1.9rem)}p{margin:0;color:#53657e;line-height:1.55}.provider{font-weight:800;color:#27724b}.reference{font:700 .76rem ui-monospace,SFMono-Regular,monospace;color:#718197;overflow-wrap:anywhere}@keyframes spin{to{transform:rotate(360deg)}}@media(prefers-reduced-motion:reduce){.ring{animation-duration:2.4s}}</style></head>
<body><main><span class="provider">{{.Provider}}</span><span class="ring" aria-hidden="true"></span><h1>Waiting for external confirmation</h1><p>Confirm in the external simulator application. This window closes automatically when the payment status changes.</p><span class="reference">{{.Reference}}</span></main></body></html>`))
