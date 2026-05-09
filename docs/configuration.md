# Configuration

prox uses [JSON5](https://json5.org) for configuration — a superset of JSON with comments, trailing commas, and unquoted keys.

## Structure

Every config has three sections:

```
┌──────────────────────────────────────────────────────┐
│                    config.json5                      │
├──────────────┬──────────────┬────────────────────────┤
│   services   │   actions    │      resources         │
│   (WHAT)     │   (HOW)      │      (WITH WHAT)       │
│              │              │                        │
│  listen addr │  type: proxy │  inline text           │
│  tls on/off  │  type: static│                        │
│  routes[]    │  type: serve │                        │
│   └ match    │  timeout     │                        │
│   └ action ──│──► ref ──────│──► body_ref → resource │
└──────────────┴──────────────┴────────────────────────┘
```

**Key concept:** everything is reference-based. Routes point to actions by name, actions point to resources by name. But you can also inline them directly when a definition isn't reused.

## Services

A service is a listener with routing rules.

```json5
{
  services: {
    my_site: {
      listen: ":8080",         // required
      tls: true,               // optional, default: false
      tls_cert: "/path/cert",  // required if tls: true
      tls_key: "/path/key",    // required if tls: true
      routes: [...]            // required, at least one
    },
  },
}
```

## Routes

Routes are evaluated in order — first match wins.

```json5
{
  match: {
    path: "/api/*", // exact or wildcard
    methods: ["GET", "POST"], // optional, empty = all
  },
  action: "proxy_to_backend", // string ref to actions map
}
```

### Inline actions

Instead of referencing a named action, you can define one inline:

```json5
{
  match: { path: "/health" },
  action: {
    type: "static",
    status: 200,
    body_ref: { text: "OK" }, // inline resource too!
  },
}
```

## Actions

### `proxy` — Reverse Proxy

| Field      | Type   | Required | Description                           |
| ---------- | ------ | -------- | ------------------------------------- |
| `type`     | string | ✓        | `"proxy"`                             |
| `upstream` | string | ✓        | `"host:port"` or `"http://host:port"` |
| `timeout`  | string |          | `"5s"`, `"30s"`, `"1m"`               |

### `static` — Static Response

| Field      | Type            | Required | Description                                 |
| ---------- | --------------- | -------- | ------------------------------------------- |
| `type`     | string          | ✓        | `"static"`                                  |
| `status`   | int             | ✓        | HTTP status code                            |
| `headers`  | object          |          | Response headers                            |
| `body_ref` | string / object |          | Ref to resource or inline `{ text: "..." }` / `{ json: {...} }` |

### `serve` — File Server

Serves files from a directory or a single file.

| Field  | Type   | Required | Description                                |
| ------ | ------ | -------- | ------------------------------------------ |
| `type` | string | ✓        | `"serve"`                                  |
| `root` | string | ✗†       | Directory to serve (e.g. `"./public"`)     |
| `file` | string | ✗†       | Single file to serve (e.g. `"./app.html"`) |

† Exactly one of `root` or `file` is required.

**Directory mode** (`root`):

- Automatically serves `index.html` for directory requests
- `GET /` → `root/index.html`
- `GET /css/app.css` → `root/css/app.css`
- Directory listings are disabled (404 if no `index.html`)
- Route prefix is stripped automatically: route `/static/*` with root `./public` maps `/static/app.css` → `./public/app.css`

**File mode** (`file`):

- Always serves the same file regardless of the request path
- Useful for SPA fallbacks

```json5
// Directory serving
{
  match: { path: "/*" },
  action: {
    type: "serve",
    root: "./public",
  },
}

// Single file
{
  match: { path: "/app/*" },
  action: {
    type: "serve",
    file: "./dist/index.html",  // SPA fallback
  },
}
```

## Resources

Named, reusable content blobs referenced by actions via `body_ref`.

| Field  | Type   | Description |
|--------|--------|-------------|
| `text` | string | Raw text content |
| `json` | any    | JSON value — auto-marshaled to a JSON string |

Use `text` for plain strings, `json` for structured data (avoids manual escaping).

```json5
{
  resources: {
    greeting: {
      text: "Hello, World!",
    },
    health: {
      json: { status: "ok", version: "1.0" },
    },
  },
}
```

Inline resources work the same way:

```json5
{
  match: { path: "/health" },
  action: {
    type: "static",
    status: 200,
    headers: { "Content-Type": "application/json" },
    body_ref: { json: { status: "ok" } },
  },
}
```

## Validation

Validate before deploying:

```bash
prox validate -config config.json5
```

The validator checks:

- All action references resolve
- All resource references resolve
- Required fields are present
- HTTP methods are valid
- Path patterns are well-formed
- TLS cert/key are provided when TLS is enabled
- Reports **all** issues at once
