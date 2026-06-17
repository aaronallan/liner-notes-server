# liner-notes-server

Backend service for **Liner Notes**, the vinyl-record identification app.

The mobile client identifies a track via ShazamKit (yielding **title and
artist**, and occasionally an ISRC); this stateless HTTP backend resolves that to
a Spotify track and returns its audio characteristics and album art. It also
maintains a Postgres corpus of tracks for harmonic **mix-matching**.

## Pipeline

```
title + artist  (optional ISRC)
  → Spotify Search  (GET /search?type=track, Client Credentials auth)
  → Spotify track ID  (+ album art)
  → ReccoBeats       (GET /v1/audio-features?ids={spotifyId} — free, no API key)
  → audio features
```

Title + artist is the primary key (ShazamKit rarely returns an ISRC); an ISRC,
when present, is used opportunistically to pin the exact recording. Spotify's own
`/audio-features` endpoint was deprecated on 2024-11-27 and returns `403` for new
apps, so [ReccoBeats](https://reccobeats.com) recovers audio features from a
Spotify track ID.

## Endpoints

| Method & path          | Purpose                                                                                                                  |
| ---------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/lookup`      | Resolve a scan (title/artist, optional ISRC) to a Spotify ID, audio features, and album art.                          |
| `POST /v1/mix-matches` | Given a scan, return corpus tracks that mix well: harmonic key (Camelot), ±5% tempo incl. half/double, ranked by loudness closeness. Requires `DATABASE_URL`. |
| `GET /healthz`         | Liveness probe.                                                                                                       |

## Configuration

Secrets are sourced from the environment (never hardcoded). See `.env.example`.

| Variable                | Description                                   |
| ----------------------- | --------------------------------------------- |
| `SPOTIFY_CLIENT_ID`     | Spotify app client ID (Client Credentials)    |
| `SPOTIFY_CLIENT_SECRET` | Spotify app client secret                     |
| `PORT`                  | HTTP listen port (default `8080`)             |
| `DATABASE_URL`          | Postgres connection string (optional). When unset, an in-memory cache is used and the mix-match corpus/endpoint are disabled. |

## Development

```sh
go test ./...        # run the test suite (store/ingest integration tests skip
                     # unless TEST_DATABASE_URL points at a Postgres)
go run ./cmd/server  # start the HTTP server
```

### With Postgres (Docker)

```sh
docker compose up --build   # app on :8080, Postgres on :5433
```

Migrations run automatically on startup. To run the integration tests against a
database:

```sh
TEST_DATABASE_URL="postgres://liner:liner@localhost:5433/liner" go test ./...
```

## Deployment (Render + Neon)

The repo ships a `render.yaml` Blueprint defining **staging** and **prod** web
services (both on Render's free plan). Postgres is hosted on **Neon** (free tier:
persistent, scales to zero), so it is not part of the Blueprint. No secrets are
committed — `DATABASE_URL` and the Spotify credentials are `sync: false`, set in
the Render dashboard.

One-time setup:

1. **Neon**: create a project and two databases (or two branches) — one for
   staging, one for prod. Copy each connection string (includes `sslmode=require`).
2. **Spotify**: create two apps (staging + prod) so quotas are isolated.
3. **Render**: create an account, connect this GitHub repo, then **New →
   Blueprint** and select it. Render reads `render.yaml` and creates both web
   services.
4. For each service, set its env vars: `DATABASE_URL` to the matching Neon
   connection string, and `SPOTIFY_CLIENT_ID` / `SPOTIFY_CLIENT_SECRET` to the
   matching Spotify app.

Deploy flow: push to `staging` → the staging service deploys (target for
release/TestFlight builds); push to `master` → prod deploys. Each service runs
the embedded migrations on boot and is health-checked at `/healthz`.

## Reliability

The Spotify Web API has no SLA. All upstream calls are best-effort: immutable
lookups are cached, transient failures are retried with backoff, and the service
degrades gracefully — returning Shazam metadata with features marked
`unavailable` rather than failing a scan.
