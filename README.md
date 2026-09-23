# am-import

[![CI](https://github.com/moudlajs/am-import/actions/workflows/ci.yml/badge.svg)](https://github.com/moudlajs/am-import/actions/workflows/ci.yml)

Turn a plain text file of songs into an Apple Music library playlist.

```text
# road trip.txt
Björk - Army of Me
Portishead - Glory Box
Massive Attack - Teardrop
```

```sh
am-import -name "Road trip" "road trip.txt"
```

> **Status:** under construction. Tracked in the
> [M1 — MVP milestone](https://github.com/moudlajs/am-import/milestones).

## Why no Apple Developer account?

The official route (MusicKit) needs a paid Apple Developer membership to mint a
developer token. The Apple Music web player at music.apple.com already has one,
and it has your user token while you are signed in. `am-import` reuses those two
tokens and calls the same endpoints the web player calls. You supply the
tokens; the tool never logs in, scrapes or obtains them itself.

## Disclaimer

- These are **undocumented, private endpoints**. Apple can change or remove
  them at any time and this tool will break when they do.
- It is meant for **personal use with your own account**. Don't use it to
  automate anyone else's library, and don't hammer the API. Searches are
  deliberately sequential and rate-limited.
- Not affiliated with or endorsed by Apple.

## Install

```sh
go install github.com/moudlajs/am-import/cmd/am-import@latest
```

Or download a binary for macOS or Linux from
[Releases](https://github.com/moudlajs/am-import/releases) and check it
against `checksums.txt`.

## Token setup

1. Open <https://music.apple.com> and sign in.
2. Open DevTools (<kbd>Cmd</kbd>+<kbd>Opt</kbd>+<kbd>I</kbd>) → **Network** tab.
3. Type `amp-api` in the filter box, then click around (open your Library) so
   requests to `amp-api.music.apple.com` appear.
4. Click one of them → **Headers** → **Request Headers** and copy:
   - `authorization` → `AM_DEV_TOKEN` (drop the `Bearer ` prefix)
   - `media-user-token` → `AM_USER_TOKEN`
5. `cp .env.example .env` and paste them in, or export them in your shell
   (shell variables win over `.env`).

`.env` is gitignored. The tokens grant access to your Apple Music library, so
treat them like a password. `am-import` never prints them.

When the tool exits with code 2, your user token has expired. Repeat the steps
above.

## Usage

```text
am-import -name "Playlist name" [-storefront cz] [-dry-run] [-playlist-id ID] [-delay 500ms] [-v] <file.txt>
```

Input format: one `Artist - Title` per line. Blank lines and lines starting
with `#` are ignored. A line without ` - ` is searched as-is.

Full usage and exit codes will be documented here once implemented.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). `make lint test` runs what CI runs.

## License

[MIT](LICENSE)
