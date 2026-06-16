# liner-notes-server

Backend service for **Liner Notes**, the vinyl-record identification app.

The mobile client identifies a track via ShazamKit (yielding **title, artist, and
ISRC**); this stateless HTTP backend takes that ISRC and returns the track's
musical/audio characteristics.

## Pipeline

```
ISRC
  → Spotify Search  (GET /search?type=track&q=isrc:{isrc}, Client Credentials auth)
  → Spotify track ID
  → ReccoBeats       (GET /v1/track/{spotifyId}/audio-features — free, no API key)
  → audio features
```

Spotify's own `/audio-features` endpoint was deprecated on 2024-11-27 and returns
`403` for new apps, so [ReccoBeats](https://reccobeats.com) is used to recover
audio features from a Spotify track ID.

## Configuration

Secrets are sourced from the environment (never hardcoded). See `.env.example`.

| Variable                | Description                                   |
| ----------------------- | --------------------------------------------- |
| `SPOTIFY_CLIENT_ID`     | Spotify app client ID (Client Credentials)    |
| `SPOTIFY_CLIENT_SECRET` | Spotify app client secret                     |
| `PORT`                  | HTTP listen port (default `8080`)             |

## Development

```sh
go test ./...        # run the test suite
go run ./cmd/server  # start the HTTP server
```

## Reliability

The Spotify Web API has no SLA. All upstream calls are best-effort: immutable
lookups are cached, transient failures are retried with backoff, and the service
degrades gracefully — returning Shazam metadata with features marked
`unavailable` rather than failing a scan.
