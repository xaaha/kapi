# kapi hub

A Cloudflare Worker that keeps read-only snapshots of your kapi pages in an R2
bucket. `kapi publish <page>` uploads one; the hub lists your pages and every
revision of each. You deploy your own hub on your own Cloudflare account and
domain. Nothing here names a specific account or hostname.

## Routes

| Method and path | Who | What |
|---|---|---|
| `PUT /api/pages/{id}/revs/{rev}` | the kapi CLI, with `Authorization: Bearer <token>` | stores the HTML snapshot and makes it the page's latest revision |
| `GET /` | you, through Cloudflare Access | lists published pages, newest first |
| `GET /p/{id}/` | you | the latest revision |
| `GET /p/{id}/revs/` | you | every revision, newest first |
| `GET /p/{id}/revs/{rev}/` | you | one revision |

`{id}` is the 16-hex page id kapi already uses. Served pages get a
Content-Security-Policy with `default-src 'none'`, so no script on a published
page ever runs. Uploads are text/html and at most 10 MiB.

The Worker checks the bearer token itself. It does not check who is reading:
put the read routes behind Cloudflare Access (step 5). Without Access, anyone
who knows the hostname can read every page.

## Deploy

You need a Cloudflare account with a domain on it, R2 enabled, and
[wrangler](https://developers.cloudflare.com/workers/wrangler/). Run these from
this `hub/` directory.

1. Log in.

   ```sh
   npx wrangler login
   ```

2. Create the bucket. Any name works; it must match `bucket_name` in
   `wrangler.toml`.

   ```sh
   npx wrangler r2 bucket create kapi-pages
   ```

3. Edit `wrangler.toml`: set `pattern` under `routes` to the hostname you want,
   for example `pages.your-domain.com`, and `bucket_name` if you changed it.
   Keep `workers_dev = false` and `preview_urls = false`. A `*.workers.dev`
   address would serve your pages without going through Access.

4. Make an upload token, store it as the Worker secret, and deploy.

   ```sh
   openssl rand -base64 32 > ~/.kapi/hub-token
   chmod 600 ~/.kapi/hub-token
   npx wrangler secret put KAPI_HUB_TOKEN < ~/.kapi/hub-token
   npx wrangler deploy
   ```

   Until the secret is set, every upload gets 503.

5. Put reads behind Cloudflare Access. In the Cloudflare dashboard, under
   Zero Trust, Access, Applications:

   - Add a self-hosted application for your hub hostname with path empty, and
     an Allow policy that names you (for example, your email address).
   - Add a second self-hosted application for the same hostname with path
     `api/`, and a policy whose action is Bypass for Everyone. Access applies
     the most specific path, so uploads reach the Worker, which checks the
     bearer token. Browsers that open `/api/...` only get 404 or 405.

   Open `https://<your hostname>/` in a private window. You should see the
   Access login, not the page list.

## Point kapi at the hub

Write `$KAPI_HOME/hub.json` (`~/.kapi/hub.json` by default):

```json
{"url": "https://pages.your-domain.com", "token_file": "hub-token"}
```

`token_file` is relative to `$KAPI_HOME`, absolute, or starts with `~/`. The
environment overrides the file: `KAPI_HUB_URL` and `KAPI_HUB_TOKEN`. The URL
must be https, except for `localhost` and loopback addresses.

```sh
kapi publish plan.html    # prints https://pages.your-domain.com/p/<id>/
```

Failed uploads are retried on network errors, 408, 429 and 5xx, with backoff,
up to five attempts. Other 4xx answers fail at once. A redirect fails at once
too: it usually means Access is intercepting `/api/`, so check step 5.

## Develop

The tests use Node's built-in runner and a fake R2 bucket. Nothing to install.

```sh
npm test
```

To run the Worker locally, put `KAPI_HUB_TOKEN=dev-token` in `.dev.vars` and
run `npx wrangler dev`, then point kapi at it with
`KAPI_HUB_URL=http://localhost:8787 KAPI_HUB_TOKEN=dev-token`.
