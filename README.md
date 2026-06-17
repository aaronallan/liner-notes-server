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

## Deployment (Render)

The repo ships a `render.yaml` Blueprint defining **staging** and **prod** web
services, each with its own managed Postgres. Secrets are never committed —
`DATABASE_URL` is injected from the managed database and Spotify credentials are
`sync: false` (set in the dashboard).

One-time setup:

1. Create a Render account and connect this GitHub repo.
2. **New → Blueprint**, select the repo. Render reads `render.yaml` and
   provisions both web services and both databases.
3. Create **two Spotify apps** (staging + prod) so quotas are isolated, and set
   each app's `SPOTIFY_CLIENT_ID` / `SPOTIFY_CLIENT_SECRET` on the matching
   Render service.

Deploy flow: push to `staging` → the staging service deploys (target for
release/TestFlight builds); push to `main` → prod deploys. Each service runs the
embedded migrations on boot and is health-checked at `/healthz`.

## Reliability

The Spotify Web API has no SLA. All upstream calls are best-effort: immutable
lookups are cached, transient failures are retried with backoff, and the service
degrades gracefully — returning Shazam metadata with features marked
`unavailable` rather than failing a scan.
