# go-bosch: the design

Research into BSH Home Connect appliances (Bosch, Thermador, Siemens, Gaggenau), a
Thermador dishwasher in view, done on 2026-11-15 from public sources, to decide what a Go
module for them would be. Nothing is built and nothing here was run against an appliance.
The decisions at the end are open.

In short: a Home Connect appliance serves a WebSocket on the LAN, and with the keys of
its own profile a client can read and drive it there with no cloud at run time. The keys
come from the owner's Home Connect account, once. BSH documents none of this; its
supported interface is a cloud API. The local protocol is written down by others, in Go
among them.

## The device

A Home Connect appliance joins the home's Wi-Fi and is paired to an account in the Home
Connect app. BSH's own description is that "direct access to home appliances is not
possible" and that its cloud API is the way in
([how it works](https://developer.home-connect.com/how_it_works)). What the appliance
sends to BSH's servers, and what the app does on the LAN, is not documented.

## Ways in

### The appliance's own WebSocket (local)

From the protocol notes of [go-homeconnect2mqtt](https://github.com/SukramJ/go-homeconnect2mqtt)
(`docs/01-protocol.md`) and from [hcpy](https://github.com/osresearch/hcpy), the first
public implementation:

- The appliance listens at `/homeconnect`, in one of two modes that its profile names
  (`connectionType`):
  - **AES**: `ws://<host>:80`, the messages encrypted in the application, AES-CBC with a
    chained HMAC-SHA256, both keys derived from the appliance's pre-shared key.
  - **TLS**: `wss://<host>:443`, TLS 1.2 with that pre-shared key. hcpy saw a dishwasher
    negotiate `ECDHE-PSK-CHACHA20-POLY1305`.
- Messages are JSON: a session id, a message id, a resource, an action, data. After a
  short handshake the appliance gives every value at once, then pushes changes.
- Read: the operation state, the door, the remaining time, errors, each under a numeric
  id that the profile's feature description names. Do: set a value, run a command, select
  and start a program; starting is where appliances differ most.
- The pre-shared key (and for AES an initial vector), with the feature descriptions, come
  from the account: [the profile downloader](https://github.com/bruestel/homeconnect-profile-downloader)
  or hcpy's own login fetch them once. That login is the account's, in a browser, with a
  CAPTCHA.
- A dishwasher reports `RemoteControlStartAllowed`: starting a cycle wants the remote
  start enabled on the appliance, which opening the door clears. Nothing found says the
  local protocol goes around it.

Go's standard library has no pre-shared-key cipher suites, so the TLS mode needs another
TLS implementation; go-homeconnect2mqtt does it with OpenSSL through cgo, behind a build
tag. The AES mode is plain Go.

### BSH's cloud API (official)

- `https://api.home-connect.com`, with a simulator at `simulator.home-connect.com`
  ([general documentation](https://api-docs.home-connect.com/general/)).
- OAuth: the authorization code flow and a device flow, scopes by appliance type
  ([authorization](https://api-docs.home-connect.com/authorization/)).
- Events as a stream per appliance.
- Limits, per client and account: 1,000 requests a day, 50 a minute, 5 program starts a
  minute, 10 token refreshes a minute and 100 a day, 10 event channels.
- Thermador is among the brands covered. Remote start has to be enabled on the appliance
  and is cleared when the door opens.

### Matter

Bosch's [United States FAQ](https://www.bosch-home.com/us/owner-support/get-support/home-connect/home-connect-faq)
says Matter is rolling out for some appliances, refrigerators first. Nothing found shows a
dishwasher or a Thermador with it.

## Two ways in, compared

| | The appliance's WebSocket | The cloud API |
|---|---|---|
| Supported by BSH | no | yes |
| Local at run time | yes | no |
| Needs | the profile's keys, fetched once from the account | a developer account, OAuth consent |
| Limits | none known | 1,000 requests a day |
| Risk | a firmware update can change the protocol; the key download depends on a login that already gained a CAPTCHA | a cloud in the path |

## What this module would be

Not decided. The options, most local first:

1. **A client of the appliance's WebSocket**, taking a downloaded profile as
   configuration. The AES mode first: the key derivation, the chained HMAC, the handshake,
   the mapping of ids to names from the profile. The TLS mode is a second, larger piece,
   since it needs pre-shared-key TLS that Go does not ship.
2. **A client of the cloud API**, small and well documented, within its daily quota.

## Open questions

- Which mode the dishwasher's profile names, AES or TLS. It decides the effort.
- Whether the command should start a cycle, or reading the state is enough: starting
  meets the remote-start button each time.
- The dishwasher's model number.

## Not verified

- Nothing above was observed on an appliance.
- No report of a Thermador dishwasher on the local protocol was found; reports for
  Thermador ovens, hoods and refrigerators were, through the Home Assistant integration
  [homeconnect_local_hass](https://github.com/chris-mc1/homeconnect_local_hass).
- Whether any Go TLS library supports the cipher suite the TLS mode needs.
- What the Matter dishwasher device type would carry; it was not re-read for this.

## Sources

- [go-homeconnect2mqtt](https://github.com/SukramJ/go-homeconnect2mqtt): the local
  protocol written out, and a Go implementation of it.
- [hcpy](https://github.com/osresearch/hcpy) and its maintained fork
  [hcpy2-0/hcpy](https://github.com/hcpy2-0/hcpy): the first implementation, the cipher
  suite seen on a dishwasher, the login with a CAPTCHA.
- [homeconnect-profile-downloader](https://github.com/bruestel/homeconnect-profile-downloader):
  fetching a profile.
- [homeconnect_local_hass](https://github.com/chris-mc1/homeconnect_local_hass): local
  use in Home Assistant, Thermador reports.
- Home Connect: [how it works](https://developer.home-connect.com/how_it_works),
  [general documentation](https://api-docs.home-connect.com/general/),
  [authorization](https://api-docs.home-connect.com/authorization/).
- [Bosch's Home Connect FAQ](https://www.bosch-home.com/us/owner-support/get-support/home-connect/home-connect-faq):
  Matter in North America.
