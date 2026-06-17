-- pgcrypto provides gen_random_uuid() on Postgres 12 (it is in core from 13+).
create extension if not exists pgcrypto;

-- id_cache is the lookup resolution cache: normalized (title, artist) -> Spotify
-- ID. It holds only identifier data, so it is safe to persist (Spotify Terms).
create table if not exists id_cache (
    norm_key   text primary key,
    spotify_id text not null,
    created_at timestamptz not null default now()
);

-- tracks is the mix-match corpus identity table, keyed on the Spotify ID. Title
-- and artist are ingestion-sourced (never Spotify-enriched). isrc is optional.
create table if not exists tracks (
    id         uuid primary key default gen_random_uuid(),
    spotify_id text unique not null,
    isrc       text,
    title      text not null,
    artist     text not null,
    source     text,
    created_at timestamptz not null default now()
);

-- mix_features holds the ReccoBeats-derived mixing signals plus the precomputed
-- Camelot code, used by harmonic/tempo/loudness matching.
create table if not exists mix_features (
    track_id   uuid primary key references tracks (id) on delete cascade,
    tempo      double precision not null,
    key        smallint not null,
    mode       smallint not null,
    loudness   double precision not null,
    camelot    text not null,
    fetched_at timestamptz not null default now()
);

create index if not exists mix_features_tempo_idx on mix_features (tempo);
create index if not exists mix_features_camelot_idx on mix_features (camelot);
