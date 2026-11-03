# Kipet Web

React + TypeScript + Vite client for the Go API. All public requests use the current origin; the development proxy forwards `/api` to `http://127.0.0.1:8080`.

```powershell
npm.cmd ci
npm.cmd run dev
```

Local UI: `http://127.0.0.1:5173`. Production output: `dist/`, built with `npm.cmd run build`. The production server must proxy `/api` to the Go API and serve `index.html` for client routes. No provider credentials belong in this directory.

`npm.cmd test` checks path encoding, bounded polling with provider delays, and cancellation. Browser integration is verified against the running API separately.

The client uses the character/equipment/statistics/talents/realms/jobs/refresh contract in `api/openapi/mvp-v1.yaml`. `/api/v1/runtime` supplies `provider_mode` and a list of `demo_characters`. A fixture server is clearly labelled; the browser never substitutes invented data after a failed live request.

Character links store `region`, canonical `realm`, `name`, and optional `tab` query parameters. UI locale is stored independently in local storage. Polling stops after terminal status, after one bounded recovery from a missing job, or after two minutes; the job can then be checked manually.
