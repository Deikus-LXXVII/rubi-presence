# Location for Rubi

A [Rubi](https://github.com/Deikus-LXXVII/rubi) plugin that lets your AI agent know whether you're at
home, at work or at another place you name, and be told when you arrive or leave. It never gets
coordinates.

- `presence_where`: which of your places you're at, or the last one you left;
- `presence_history`: recent arrivals and departures;
- `presence_watch`: "wake me when they get home", for reminders and the like (one-time by default);
- events `arrived` and `left` for Bots subscribed to Location.

## How it works

Your iPhone decides where you are. You make Shortcuts automations ("When I arrive at Home", "When I leave
Work") that call private web addresses Rubi gives you, one per place and event. A request carries
nothing but its address, so Rubi Gateway, Cloudflare and anything else in between only learn that one
of your random addresses was called, not where you are. Rubi turns it into "arrived at Home".

Hook addresses need Rubi Gateway (the default) or Tailscale as Rubi's transport.

## Install

Ask your agent: "Set up Location in Rubi." It installs the plugin (you approve), then sends you a link.
Connect it, then open its settings in the Rubi panel:

1. Name your places, one per line. The first one is your home.
2. Choose what your agent may know: which of your places you're at, or only whether you're at home.
3. For each address under *Addresses for iPhone Shortcuts*: in the Shortcuts app, *Automation* > *New
   Automation* > *Arrive* (or *Leave*) > choose the place > *Run Immediately* > *Next* > *New Blank
   Automation* > add *Get Contents of URL*, paste the address, tap the arrow and set *Method* to *POST*.

iPhone geofences can be late, and now and then miss an event; your agent is told so. Keep the addresses
private: anyone who has one can report that event.

## Permissions

| | |
|---|---|
| Asks for | nothing |
| See where you are (place names only) | no approval by default |
| Be told when you arrive or leave (wakes your agent) | no approval by default |
| Connects to | nothing; it only receives requests at its addresses |
| Can notify your agent about | arrivals and departures |

## Development

```bash
go test ./...
go build -o presence . && ./presence --manifest
```

To develop against a local Rubi checkout, use a workspace (not committed): `go work init . ../rubi`.

## Releasing

Tag `vX.Y.Z`. The release workflow tests, builds, writes `rubi-plugin.json`, signs `SHA256SUMS` with the
plugin key (`PLUGIN_SIGNING_KEY`; public half in `publisher-key.pub.pem`), attests the build, and
publishes the release.

## License

[MIT](LICENSE)
