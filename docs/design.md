# go-bosch: the design

Research into BSH Home Connect appliances (Bosch, Thermador, Siemens, Gaggenau), a
Thermador dishwasher in view, done on 2026-11-15 from public sources, and the module it
led to. So far the module finds appliances on the network, which was run against one
dishwasher; the protocol itself was not.

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
- An appliance announces itself over DNS-SD as `_homeconnect._tcp`, with a TXT record
  that carries its Home Connect id (`id`), its brand, its kind (`type`) and its model as
  BSH abbreviates it (`vib`): what the Home Assistant integration
  [reads to find one](https://github.com/chris-mc1/homeconnect_local_hass/blob/baa64d14a53c5e4777512117790b81d272361858/custom_components/homeconnect_ws/config_flow.py).
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

## What this module is

A client of the appliance's own WebSocket, local at run time, built in steps. The cloud
API is not taken: it puts BSH's cloud in the path of every reading, under a daily quota.

### Shape

- `homeconnect`: the library. So far, an appliance's announcement: `Recognize` says
  whether an instance of `_homeconnect._tcp` is an appliance's and which, and `Find` is
  the search, through a `Browser` the caller lends. Standard library only.
- `cmd/homeconnect` and `internal/cli`: the command, kong under primordium's process
  setup, with the Multicast DNS appliances are searched for over. `discover` prints the
  appliances that announce themselves; `discover --whole` prints all each one announces.
- `internal/terminal`: every line the command prints goes through it, so that what an
  appliance wrote is shown and never obeyed.

### Decisions

- **Finding an appliance needs a means this module does not have, and does not take.** A
  host has one Multicast DNS responder, and it belongs to the program, not to a library.
  `Recognize` is the knowledge, with no socket; `Find` is the search, through a `Browser`
  declared here in this module's own terms, which names no Multicast DNS library. The
  command has the program's, [go-mdns](https://github.com/forkcloser/go-mdns), and a
  lint rule keeps it to `internal/cli`.
- **An appliance is its Home Connect id.** It is what the announcement carries and what a
  profile names the appliance by. An instance of the service with no id is not taken for
  an appliance.
- **An announcement is a claim.** Anyone on the network can announce an id at an address
  of their choosing. What opens an appliance is the key of its profile, which is not on
  the network.
- **The announced port is kept as announced.** The two modes listen on two ports, 80 and
  443, and what an appliance announces may say which it speaks: not known until one is
  seen, so `Found` gives each address with the port that came with it, and nothing is
  read into it yet.
- **Discovery first.** What the dishwasher in view announces, its port above all, says
  which of the two modes the rest has to speak, and so what is built next.

### Next

1. The wire: the session, the messages and the handshake, with the AES mode's
   encryption, which is plain Go.
2. The TLS mode, if the appliance in view needs it: pre-shared-key TLS, which Go's
   standard library does not have.
3. The profile: the key, and the descriptions that name each numeric id.
4. Fetching the profile from the account.

## Observed on an appliance

On 2026-11-22 the command's `discover --whole` was run on a network with one Home
Connect appliance, a Thermador dishwasher, and heard it within its wait:

- It announces one instance of `_homeconnect._tcp`, at one IPv4 address and port 80.
- Its TXT record has eight entries: `id`, `brand`, `type` and `vib`, as the Home
  Assistant integration reads them; and `info`, the kind again in capitals; `mac`, twelve
  hexadecimal digits; `txtvers`, 2; and `vers`, `1.0-2.17.1.13549`.
- Its `id` is its brand, its `vib` and its `mac`, joined by dashes, and `vib` is the
  model its owner had given, `DWHD650WFM`. Its host's name is its `id`, then `.local`.

Port 80 is where the AES mode listens, and a host named by the id alone is what the
notes of go-homeconnect2mqtt give as an AES appliance's default host, where a TLS one's
has the brand and the kind before the id. So this dishwasher looks to speak AES, the
mode Go's standard library can speak. An announcement does not prove it: the profile's
`connectionType` says which, and so does the port that answers.

## Open questions

- Which mode the dishwasher speaks, AES or TLS: its announcement points to AES, see
  [Observed on an appliance](#observed-on-an-appliance). It decides the effort.
- Whether the command should start a cycle, or reading the state is enough: starting
  meets the remote-start button each time.

## Not verified

- Nothing of the protocol itself was observed on an appliance: an announcement was, and
  nothing past it.
- What an appliance in the TLS mode announces, its port above all.
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
